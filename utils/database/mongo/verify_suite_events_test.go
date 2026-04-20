package manager

import (
	"os"
	"testing"

	"haruki-suite/utils/sekai"
)

func TestRealSuiteUserEvents(t *testing.T) {
	keyHex := "6732666343305A637A4E394D544A3631"
	ivHex := "6D737833495630693958453575595A31"

	cryptor, err := sekai.NewSekaiCryptorFromHex(keyHex, ivHex)
	if err != nil {
		t.Fatalf("failed to create cryptor: %v", err)
	}

	data, err := os.ReadFile("../../../8D6B082F-4898-4B41-AC0D-7AC9ABA31015")
	if err != nil {
		t.Skipf("test suite file not found: %v", err)
	}

	unpacked, err := cryptor.Unpack(data)
	if err != nil {
		t.Fatalf("failed to unpack: %v", err)
	}

	suiteMap, ok := unpacked.(map[string]any)
	if !ok {
		t.Fatalf("unpacked data is not map[string]any, got %T", unpacked)
	}

	events := extractAnySlice(suiteMap["userEvents"])
	if len(events) == 0 {
		t.Fatal("userEvents is empty or missing in suite data")
	}
	t.Logf("Found %d userEvents in suite data", len(events))

	// Verify every event can be normalized and has extractable eventId
	for i, ev := range events {
		m, ok := normalizeDocument(ev)
		if !ok {
			t.Errorf("event[%d]: normalizeDocument failed for type %T", i, ev)
			continue
		}
		eid, ok := getRequiredInt(m, fieldEventID)
		if !ok {
			t.Errorf("event[%d]: getRequiredInt(eventId) failed, type=%T", i, m["eventId"])
			continue
		}
		ept := getInt(m, fieldEventPoint)
		_, hasRank := m[fieldEventRank]
		t.Logf("  event[%d]: eventId=%d (type=%T) eventPoint=%d hasRank=%v", i, eid, m["eventId"], ept, hasRank)
	}

	// Simulate merge with stale DB data (only old event 199)
	oldData := map[string]any{
		"userEvents": []any{
			map[string]any{
				"eventId":    int64(199),
				"eventPoint": int64(1637844),
			},
		},
	}

	merged := mergeUserEvents(oldData, suiteMap)
	if merged == nil {
		t.Fatal("mergeUserEvents returned nil — events silently dropped (the bug)")
	}

	eventMap := make(map[int64]map[string]any)
	for _, ev := range merged {
		m, _ := ev.(map[string]any)
		eid, _ := toInt64(m["eventId"])
		eventMap[eid] = m
	}

	// Old event 199 must be preserved
	if _, ok := eventMap[199]; !ok {
		t.Error("old event 199 lost after merge")
	}

	// New events from suite must be present (event 200 in this snapshot)
	if len(merged) < 2 {
		t.Errorf("expected at least 2 events after merge (old 199 + new), got %d", len(merged))
	}

	t.Logf("Merge result: %d events total", len(merged))
	for eid, m := range eventMap {
		pt := getInt(m, fieldEventPoint)
		_, hasRank := m[fieldEventRank]
		t.Logf("  eventId=%d eventPoint=%d hasRank=%v", eid, pt, hasRank)
	}
}
