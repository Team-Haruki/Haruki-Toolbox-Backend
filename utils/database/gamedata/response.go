package gamedata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// emptyArray is what a requested-but-absent suite key renders as. It is not a
// stylistic choice: the MongoDB path returns bson.A{} from GetValueFromResult
// for a key the projection did not find, and clients depend on it.
var emptyArray = []byte("[]")

// userGamedataAllowedFields mirrors utils/api/data. The whole userGamedata
// object is stored, but only these seven fields are ever served: the rest of it
// is account-identifying. The column is ~200 bytes, so filtering it in Go costs
// nothing measurable even though every other key moves as raw bytes.
var userGamedataAllowedFields = []string{"userId", "name", "deck", "exp", "totalExp", "coin", "rank"}

const (
	userGamedataKey = "userGamedata"
	// Derived fields NormalizeProviderResponse synthesises. They are not stored
	// anywhere: a response carrying a top-level `_id` also carries `_idString`,
	// and a NESTED userGamedata carrying `userId` also carries `userIdString`.
	// They exist because these ids exceed 2^53 and a JavaScript client that
	// parses the numeric form silently loses precision.
	idStringKey     = "_idString"
	userIDStringKey = "userIdString"
	userIDKey       = "userId"
	idKey           = "_id"
)

// idStringFromRaw mirrors providerIDString: an integral number or a non-empty
// string yields its decimal text; a fractional or non-numeric value yields
// nothing, and the derived field is then omitted rather than guessed.
func idStringFromRaw(raw []byte) (string, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return "", false
	}
	if t[0] == '"' {
		var str string
		if err := json.Unmarshal(t, &str); err != nil || str == "" {
			return "", false
		}
		return str, true
	}
	var num json.Number
	if err := json.Unmarshal(t, &num); err != nil {
		return "", false
	}
	if i, err := num.Int64(); err == nil {
		return strconv.FormatInt(i, 10), true
	}
	f, err := num.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return "", false
	}
	return strconv.FormatFloat(f, 'f', 0, 64), true
}

// SuiteBody renders a suite response body.
//
// unwrapSingle must be true only when the caller supplied an explicit ?key= with
// exactly one segment. That distinction is load-bearing: with no ?key= the keys
// come from the allowlist, and a one-entry allowlist must still produce an
// object, not a bare value.
func (r *Row) SuiteBody(keys []string, unwrapSingle bool) ([]byte, error) {
	if unwrapSingle && len(keys) == 1 {
		return r.suiteBareValue(keys[0])
	}
	out := make([]byte, 0, 1024)
	out = append(out, '{')
	first := true
	for _, key := range keys {
		if key == userGamedataKey {
			// Asymmetry preserved from buildSuiteResponse: an absent
			// userGamedata is OMITTED, while any other absent key renders [].
			v, ok, err := r.userGamedata(true)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			out = appendMember(out, &first, key, v)
			continue
		}
		v, ok, err := r.RawValue(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			v = emptyArray
		}
		out = appendMember(out, &first, key, v)
	}
	return append(out, '}'), nil
}

func (r *Row) suiteBareValue(key string) ([]byte, error) {
	if key == userGamedataKey {
		v, ok, err := r.userGamedata(false)
		if err != nil {
			return nil, err
		}
		if !ok {
			// The MongoDB path returns an empty DOCUMENT here, not an empty
			// array, because it falls back to bson.D{}.
			return []byte("{}"), nil
		}
		return v, nil
	}
	v, ok, err := r.RawValue(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return emptyArray, nil
	}
	return v, nil
}

// MysekaiBody renders a mysekai response body.
//
// Two differences from suite, both existing behaviour rather than choices:
// a single key is NOT unwrapped to a bare value, and an absent key is OMITTED
// rather than rendered as [].
func (r *Row) MysekaiBody(keys []string) ([]byte, error) {
	if len(keys) == 0 {
		return r.wholeDocument(false)
	}
	out := make([]byte, 0, 1024)
	out = append(out, '{')
	first := true
	for _, key := range keys {
		v, ok, err := r.RawValue(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = appendMember(out, &first, key, v)
		out = appendIDString(out, &first, key, v)
	}
	return append(out, '}'), nil
}

// appendIDString adds the derived `_idString` beside a top-level `_id`, exactly
// as NormalizeProviderResponse does. Omitting it drops a field every existing
// client sees — and it is the field that carries the id safely, because the
// numeric form exceeds what a JavaScript number represents.
func appendIDString(dst []byte, first *bool, key string, raw []byte) []byte {
	if key != idKey {
		return dst
	}
	str, ok := idStringFromRaw(raw)
	if !ok {
		return dst
	}
	return appendMember(dst, first, idStringKey, quoteJSONString(str))
}

// PrivateBody renders the private-surface body: the whole row including `_id`
// and `server`, or a projection of it.
func (r *Row) PrivateBody(keys []string) ([]byte, error) {
	if len(keys) == 0 {
		return r.wholeDocument(true)
	}
	if len(keys) == 1 {
		v, ok, err := r.RawValue(keys[0])
		if err != nil {
			return nil, err
		}
		if !ok {
			// The private surface answers null, not [], for a key it cannot
			// find — including a dotted path, which it never descended.
			return []byte("null"), nil
		}
		return v, nil
	}
	out := make([]byte, 0, 1024)
	out = append(out, '{')
	first := true
	for _, key := range keys {
		v, ok, err := r.RawValue(key)
		if err != nil {
			return nil, err
		}
		if !ok {
			v = []byte("null")
		}
		out = appendMember(out, &first, key, v)
		if ok {
			out = appendIDString(out, &first, key, v)
		}
	}
	return append(out, '}'), nil
}

// wholeDocument renders every key this row carries. withIdentity includes `_id`
// and `server`, which the private surface returns and the public ones do not.
func (r *Row) wholeDocument(withIdentity bool) ([]byte, error) {
	out := make([]byte, 0, 4096)
	out = append(out, '{')
	first := true

	if withIdentity {
		idRaw := []byte(fmt.Sprintf("%d", r.UserID))
		out = appendMember(out, &first, idKey, idRaw)
		if str, ok := idStringFromRaw(idRaw); ok {
			out = appendMember(out, &first, idStringKey, quoteJSONString(str))
		}
		if sv, err := json.Marshal(r.Server); err == nil {
			out = appendMember(out, &first, catalog.ColServer, sv)
		}
	}
	if r.HasUpload {
		out = appendMember(out, &first, catalog.ColUploadTime,
			[]byte(fmt.Sprintf("%d", r.UploadTime)))
	}

	var flattened []byte
	flatFirst := true
	hasFlattened := false

	for i := range r.cat.Entries {
		e := &r.cat.Entries[i]
		raw, present := r.byColumn[e.Column]
		if !present {
			continue
		}
		v := raw
		if e.Storage == catalog.StorageCompactJSON {
			expanded, err := ExpandCompactJSON(raw)
			if err != nil {
				return nil, err
			}
			v = expanded
		}
		if e.Path != "" {
			if flattened == nil {
				flattened = append(flattened, '{')
			}
			flattened = appendMember(flattened, &flatFirst, e.Child, v)
			hasFlattened = true
			continue
		}
		if e.Key == userGamedataKey {
			filtered, ok, err := r.userGamedata(true)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			v = filtered
		}
		out = appendMember(out, &first, e.Key, v)
	}

	// `extra` members are spliced in at top level, except the re-nested
	// flattened parent, whose unknown children rejoin their siblings.
	extraFlattened := r.extraFlattenedChildren()
	for _, k := range r.extraTopLevelKeys() {
		v, _ := r.extraMember(k)
		out = appendMember(out, &first, k, v)
	}

	if hasFlattened || len(extraFlattened) > 0 {
		if flattened == nil {
			flattened = append(flattened, '{')
		}
		for _, kv := range extraFlattened {
			flattened = appendMember(flattened, &flatFirst, kv.key, kv.val)
		}
		flattened = append(flattened, '}')
		out = appendMember(out, &first, r.cat.FlattenKey, flattened)
	}

	return append(out, '}'), nil
}

type kvPair struct {
	key string
	val []byte
}

// extraFlattenedChildren returns the members stored under the flattened parent
// inside `extra` — children the catalog does not name.
func (r *Row) extraFlattenedChildren() []kvPair {
	if r.cat.FlattenKey == "" {
		return nil
	}
	raw, ok := r.extraMember(r.cat.FlattenKey)
	if !ok {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make([]kvPair, 0, len(m))
	for k, v := range m {
		out = append(out, kvPair{key: k, val: v})
	}
	return out
}

// extraTopLevelKeys returns `extra` member names excluding the flattened parent.
func (r *Row) extraTopLevelKeys() []string {
	all := r.ExtraKeys()
	if len(all) == 0 {
		return nil
	}
	out := make([]string, 0, len(all))
	for _, k := range all {
		if r.cat.FlattenKey != "" && k == r.cat.FlattenKey {
			continue
		}
		out = append(out, k)
	}
	return out
}

// userGamedata returns the stored userGamedata object filtered to the seven
// fields the API serves.
func (r *Row) userGamedata(nested bool) ([]byte, bool, error) {
	e, place := r.cat.Resolve(userGamedataKey)
	if place != catalog.PlaceColumn {
		return nil, false, nil
	}
	raw, ok := r.byColumn[e.Column]
	if !ok {
		return nil, false, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, fmt.Errorf("gamedata: decode %s: %w", userGamedataKey, err)
	}
	out := make([]byte, 0, 256)
	out = append(out, '{')
	first := true
	for _, f := range userGamedataAllowedFields {
		v, present := m[f]
		if !present {
			continue
		}
		out = appendMember(out, &first, f, v)
	}
	// Only a NESTED userGamedata gets userIdString. A bare `?key=userGamedata`
	// response does not: NormalizeProviderResponse passes objectName="" for the
	// root value, and the synthesis is gated on that name.
	if nested {
		if v, present := m[userIDKey]; present {
			if str, ok := idStringFromRaw(v); ok {
				out = appendMember(out, &first, userIDStringKey, quoteJSONString(str))
			}
		}
	}
	return append(out, '}'), true, nil
}

func quoteJSONString(s string) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		return []byte(`""`)
	}
	return b
}

// appendMember splices one `"key":value` member, inserting the separating comma
// when it is not the first.
func appendMember(dst []byte, first *bool, key string, value []byte) []byte {
	if !*first {
		dst = append(dst, ',')
	}
	*first = false
	kb, err := json.Marshal(key)
	if err != nil {
		// A Go string always marshals; keep the shape valid regardless.
		kb = []byte(`""`)
	}
	dst = append(dst, kb...)
	dst = append(dst, ':')
	return append(dst, value...)
}
