// Package gamemerge holds the three history-accumulating merges that a suite
// upload performs: userEvents, userWorldBlooms and userGachas.
//
// These keys are NOT replaced by an upload. A client only ever sends the events
// it currently knows about, so a plain overwrite would erase a player's history
// the first time they stop seeing an old event. Each merge therefore unions the
// stored value with the uploaded one and keeps, per identity, the record that
// looks more advanced.
//
// The package is deliberately storage-agnostic and has no dependencies: the same
// implementation serves the MongoDB path and the PostgreSQL one, so the cutover
// cannot introduce a behaviour difference here.
//
// ONE HAZARD, and it is silent: every value here fails CLOSED. A document whose
// shape the normalizer does not recognise, or whose identity field is missing,
// is skipped with no error and no log. If the caller hands in a decoding whose
// element types differ from the expected ones, the stored history simply looks
// empty and the next upload replaces it with the client's short list. Callers
// must decode JSON numbers with UseNumber (json.Number is accepted below) rather
// than through float64.
package gamemerge

import (
	"encoding/json"
	"strconv"
)

// Field names, as the game sends them.
const (
	KeyUserEvents      = "userEvents"
	KeyUserWorldBlooms = "userWorldBlooms"
	KeyUserGachas      = "userGachas"

	fieldEventID                = "eventId"
	fieldEventPoint             = "eventPoint"
	fieldEventRank              = "rank"
	fieldGameCharacterID        = "gameCharacterId"
	fieldWorldBloomChapterPoint = "worldBloomChapterPoint"
	fieldGachaID                = "gachaId"
	fieldGachaBehaviorID        = "gachaBehaviorId"
	fieldLastSpinAt             = "lastSpinAt"
)

// Normalizer coerces stored values into the plain Go shapes the merges work on.
//
// It is injected rather than inferred because the coercions FAIL CLOSED: an
// element whose type the normalizer does not recognise is skipped silently. The
// MongoDB path hands in bson.A / bson.D / bson.M and the PostgreSQL path hands
// in JSON-decoded []any / map[string]any, and a shared implementation that tried
// to guess would quietly drop a whole side of the merge — which is exactly the
// failure this indirection exists to make impossible.
type Normalizer interface {
	// Slice coerces a stored value to a slice, or returns nil.
	Slice(value any) []any
	// Document coerces one element to a string-keyed map.
	Document(value any) (map[string]any, bool)
}

// JSONNormalizer handles the shapes encoding/json produces.
type JSONNormalizer struct{}

func (JSONNormalizer) Slice(value any) []any                     { return AnySlice(value) }
func (JSONNormalizer) Document(value any) (map[string]any, bool) { return Document(value) }

// Keys lists the three keys this package merges.
func Keys() []string { return []string{KeyUserEvents, KeyUserWorldBlooms, KeyUserGachas} }

// IsMergedKey reports whether a key accumulates history rather than being
// replaced.
func IsMergedKey(key string) bool {
	return key == KeyUserEvents || key == KeyUserWorldBlooms || key == KeyUserGachas
}

// Events merges stored and uploaded userEvents.
//
// A nil result means "leave the stored value alone" — it is NOT an empty array.
// Writing [] here would delete a player's event history whenever an upload
// happened to contain none.
func Events(n Normalizer, oldValue, newValue any) []any {
	all := append(n.Slice(oldValue), n.Slice(newValue)...)
	latest := make(map[int64]map[string]any, len(all))
	order := make([]int64, 0, len(all))
	for _, item := range all {
		e, ok := n.Document(item)
		if !ok {
			continue
		}
		id, ok := requiredInt(e, fieldEventID)
		if !ok {
			continue
		}
		prev, exists := latest[id]
		if !exists {
			order = append(order, id)
			latest[id] = e
			continue
		}
		if shouldReplaceEvent(e, prev) {
			latest[id] = e
		}
	}
	return collect(order, latest)
}

// shouldReplaceEvent: a higher eventPoint always wins; a lower one never does.
// On a tie the record carrying `rank` wins, because rank only appears once an
// event has ended and that record is the more complete one. If both or neither
// carry rank, the incumbent stays — the merge is deliberately not "last write".
func shouldReplaceEvent(newEvent, oldEvent map[string]any) bool {
	newPoint := optionalInt(newEvent, fieldEventPoint)
	oldPoint := optionalInt(oldEvent, fieldEventPoint)
	if newPoint > oldPoint {
		return true
	}
	if newPoint < oldPoint {
		return false
	}
	_, newHasRank := newEvent[fieldEventRank]
	_, oldHasRank := oldEvent[fieldEventRank]
	return newHasRank && !oldHasRank
}

type bloomKey struct{ EventID, CharID int64 }

// WorldBlooms merges stored and uploaded userWorldBlooms, keyed by
// (eventId, gameCharacterId). Ties go to the NEW record.
func WorldBlooms(n Normalizer, oldValue, newValue any) []any {
	all := append(n.Slice(oldValue), n.Slice(newValue)...)
	latest := make(map[bloomKey]map[string]any, len(all))
	order := make([]bloomKey, 0, len(all))
	for _, item := range all {
		b, ok := n.Document(item)
		if !ok {
			continue
		}
		eventID, ok := requiredInt(b, fieldEventID)
		if !ok {
			continue
		}
		charID, ok := requiredInt(b, fieldGameCharacterID)
		if !ok {
			continue
		}
		k := bloomKey{EventID: eventID, CharID: charID}
		prev, exists := latest[k]
		if !exists {
			order = append(order, k)
			latest[k] = b
			continue
		}
		if optionalInt(b, fieldWorldBloomChapterPoint) >= optionalInt(prev, fieldWorldBloomChapterPoint) {
			latest[k] = b
		}
	}
	return collect(order, latest)
}

type gachaKey struct{ GachaID, GachaBehaviorID int64 }

// Gachas merges stored and uploaded userGachas, keyed by
// (gachaId, gachaBehaviorId). Ties go to the NEW record.
func Gachas(n Normalizer, oldValue, newValue any) []any {
	all := append(n.Slice(oldValue), n.Slice(newValue)...)
	latest := make(map[gachaKey]map[string]any, len(all))
	order := make([]gachaKey, 0, len(all))
	for _, item := range all {
		g, ok := n.Document(item)
		if !ok {
			continue
		}
		gachaID, ok := requiredInt(g, fieldGachaID)
		if !ok {
			continue
		}
		behaviorID, ok := requiredInt(g, fieldGachaBehaviorID)
		if !ok {
			continue
		}
		k := gachaKey{GachaID: gachaID, GachaBehaviorID: behaviorID}
		prev, exists := latest[k]
		if !exists {
			order = append(order, k)
			latest[k] = g
			continue
		}
		if optionalInt(g, fieldLastSpinAt) >= optionalInt(prev, fieldLastSpinAt) {
			latest[k] = g
		}
	}
	return collect(order, latest)
}

// collect renders the winners in first-seen order. The MongoDB implementation
// ranged over a map and so produced a different order on every call; keeping
// insertion order here is strictly more deterministic and is not observable
// through the API, whose array order for these keys was already arbitrary.
func collect[K comparable](order []K, latest map[K]map[string]any) []any {
	if len(latest) == 0 {
		return nil
	}
	out := make([]any, 0, len(latest))
	for _, k := range order {
		if v, ok := latest[k]; ok {
			out = append(out, v)
		}
	}
	return out
}

// AnySlice coerces a stored or decoded value to a slice. Anything else, nil
// included, yields nil — see the package note on failing closed.
func AnySlice(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = v
		}
		return out
	default:
		return nil
	}
}

// Document coerces one element to a string-keyed map.
func Document(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string]json.RawMessage:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = v
		}
		return out, true
	default:
		return nil, false
	}
}

func optionalInt(m map[string]any, key string) int64 {
	v, _ := requiredInt(m, key)
	return v
}

func requiredInt(m map[string]any, key string) (int64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	return ToInt64(v)
}

// ToInt64 coerces the numeric encodings these documents arrive in.
//
// json.Number is handled explicitly because the JSON path MUST decode with
// UseNumber: a game user id exceeds 2^53 and any float64 hop corrupts it.
func ToInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		parsed, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case json.RawMessage:
		parsed, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		parsed, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
