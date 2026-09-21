package gamemerge

import (
	json "encoding/json/v2"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/jsonvalue"
)

var jn = JSONNormalizer{}

func doc(pairs ...any) map[string]any {
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i].(string)] = pairs[i+1]
	}
	return m
}

// A nil result means "leave the stored value alone". Returning an empty array
// instead would delete a player's history the first time an upload carries none.
func TestEmptyMergeReturnsNilNotEmptySlice(t *testing.T) {
	if got := Events(jn, nil, nil); got != nil {
		t.Fatalf("Events = %#v, want nil", got)
	}
	if got := WorldBlooms(jn, nil, nil); got != nil {
		t.Fatalf("WorldBlooms = %#v, want nil", got)
	}
	if got := Gachas(jn, nil, nil); got != nil {
		t.Fatalf("Gachas = %#v, want nil", got)
	}
}

// History accumulates: an event the upload no longer mentions must survive.
func TestEventsKeepAStoredEventTheUploadOmits(t *testing.T) {
	old := []any{doc("eventId", 1, "eventPoint", 100)}
	upl := []any{doc("eventId", 2, "eventPoint", 5)}
	got := Events(jn, old, upl)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (%v)", len(got), got)
	}
}

func TestEventsHigherPointWins(t *testing.T) {
	old := []any{doc("eventId", 1, "eventPoint", 2000000)}
	upl := []any{doc("eventId", 1, "eventPoint", 1500000)}
	got := Events(jn, old, upl)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if p, _ := ToInt64(got[0].(map[string]any)["eventPoint"]); p != 2000000 {
		t.Fatalf("eventPoint = %d, want the higher 2000000", p)
	}
}

// On an equal eventPoint the record carrying `rank` wins, because rank only
// appears once the event has ended and that record is the more complete one.
func TestEventsTieGoesToTheRecordWithRank(t *testing.T) {
	old := []any{doc("eventId", 1, "eventPoint", 100)}
	upl := []any{doc("eventId", 1, "eventPoint", 100, "rank", 7)}
	got := Events(jn, old, upl)
	if _, ok := got[0].(map[string]any)["rank"]; !ok {
		t.Fatalf("tie did not prefer the ranked record: %v", got[0])
	}

	// Reverse: the stored one has rank, the upload does not — incumbent stays.
	old2 := []any{doc("eventId", 1, "eventPoint", 100, "rank", 7)}
	upl2 := []any{doc("eventId", 1, "eventPoint", 100)}
	got2 := Events(jn, old2, upl2)
	if _, ok := got2[0].(map[string]any)["rank"]; !ok {
		t.Fatalf("tie discarded the ranked incumbent: %v", got2[0])
	}
}

// Equal points must still refresh fields carried by the new snapshot.
func TestEventsTieWithNoRankRefreshesSnapshot(t *testing.T) {
	old := []any{doc("eventId", 1, "eventPoint", 100, "marker", "old")}
	upl := []any{doc("eventId", 1, "eventPoint", 100, "marker", "new")}
	got := Events(jn, old, upl)
	if got[0].(map[string]any)["marker"] != "new" {
		t.Fatalf("tie kept the stale incumbent: %v", got[0])
	}
}

// Blooms and gachas tie to the NEW record, unlike events.
func TestBloomsAndGachasTieGoesToTheNewRecord(t *testing.T) {
	oldB := []any{doc("eventId", 1, "gameCharacterId", 2, "worldBloomChapterPoint", 10, "m", "old")}
	uplB := []any{doc("eventId", 1, "gameCharacterId", 2, "worldBloomChapterPoint", 10, "m", "new")}
	if got := WorldBlooms(jn, oldB, uplB); got[0].(map[string]any)["m"] != "new" {
		t.Fatalf("bloom tie kept the incumbent: %v", got[0])
	}

	oldG := []any{doc("gachaId", 1, "gachaBehaviorId", 2, "lastSpinAt", 1000, "m", "old")}
	uplG := []any{doc("gachaId", 1, "gachaBehaviorId", 2, "lastSpinAt", 1000, "m", "new")}
	if got := Gachas(jn, oldG, uplG); got[0].(map[string]any)["m"] != "new" {
		t.Fatalf("gacha tie kept the incumbent: %v", got[0])
	}
}

// Both halves of the composite keys must matter.
func TestCompositeKeysAreNotCollapsed(t *testing.T) {
	blooms := []any{
		doc("eventId", 1, "gameCharacterId", 1, "worldBloomChapterPoint", 1),
		doc("eventId", 1, "gameCharacterId", 2, "worldBloomChapterPoint", 1),
	}
	if got := WorldBlooms(jn, blooms, nil); len(got) != 2 {
		t.Fatalf("blooms len = %d, want 2", len(got))
	}
	gachas := []any{
		doc("gachaId", 1, "gachaBehaviorId", 1, "lastSpinAt", 1),
		doc("gachaId", 1, "gachaBehaviorId", 2, "lastSpinAt", 1),
	}
	if got := Gachas(jn, gachas, nil); len(got) != 2 {
		t.Fatalf("gachas len = %d, want 2", len(got))
	}
}

// A record missing its identity field is skipped. This is the fail-closed path;
// pin it so a future change makes a deliberate decision rather than a silent one.
func TestRecordsWithoutAnIdentityAreSkipped(t *testing.T) {
	got := Events(jn, []any{doc("eventPoint", 100)}, nil)
	if got != nil {
		t.Fatalf("record without eventId was kept: %v", got)
	}
	got = WorldBlooms(jn, []any{doc("eventId", 1)}, nil)
	if got != nil {
		t.Fatalf("bloom without gameCharacterId was kept: %v", got)
	}
}

// The JSON path MUST decode with jsonvalue.Numbers. jsonvalue.Number has to be accepted, and
// a value past 2^53 has to survive.
func TestJSONNumberIdentitiesSurvive(t *testing.T) {
	var decoded []any

	if err := json.UnmarshalRead(strings.NewReader(`[{"eventId":28808221489823746,"eventPoint":5}]`), &decoded, jsonvalue.Numbers); err != nil {
		t.Fatal(err)
	}
	got := Events(jn, decoded, nil)
	if len(got) != 1 {
		t.Fatalf("len = %d; a jsonvalue.Number identity was rejected", len(got))
	}
	id, ok := ToInt64(got[0].(map[string]any)["eventId"])
	if !ok || id != 28808221489823746 {
		t.Fatalf("eventId = %d, ok=%v", id, ok)
	}
}

// float64 cannot represent 28808221489823746. Decoding without jsonvalue.Numbers is the
// mistake this asserts against, so the failure is visible rather than silent.
func TestFloat64DecodingCorruptsLargeIdentities(t *testing.T) {
	var decoded []any
	if err := json.Unmarshal([]byte(`[{"eventId":28808221489823746}]`), &decoded); err != nil {
		t.Fatal(err)
	}
	id, _ := ToInt64(decoded[0].(map[string]any)["eventId"])
	if id == 28808221489823746 {
		t.Skip("float64 happened to round-trip this value; the hazard is unchanged")
	}
	t.Logf("float64 decoding turned 28808221489823746 into %d — callers must use jsonvalue.Numbers", id)
}

func TestIsMergedKey(t *testing.T) {
	for _, k := range Keys() {
		if !IsMergedKey(k) {
			t.Fatalf("%q not reported as merged", k)
		}
	}
	if IsMergedKey("userCards") {
		t.Fatal("userCards reported as merged")
	}
}

func TestEventsTieRefreshesRankAndReward(t *testing.T) {
	old := []any{doc("eventId", 1, "eventPoint", 100, "rank", 7, "rankingRewardReceivedAt", 0)}
	uploaded := []any{doc("eventId", 1, "eventPoint", 100, "rank", 9, "rankingRewardReceivedAt", 1758686145)}
	got := Events(jn, old, uploaded)[0].(map[string]any)
	if got["rank"] != 9 || got["rankingRewardReceivedAt"] != 1758686145 {
		t.Fatalf("equal-point snapshot did not refresh: %v", got)
	}
}
