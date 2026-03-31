package manager

import (
	"encoding/json"
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestGetInt(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int64
	}{
		{name: "int", val: int(1), want: 1},
		{name: "int32", val: int32(2), want: 2},
		{name: "int64", val: int64(3), want: 3},
		{name: "float64", val: float64(4), want: 4},
		{name: "uint64", val: uint64(5), want: 5},
		{name: "string", val: "6", want: 6},
		{name: "json number", val: json.Number("7"), want: 7},
		{name: "invalid string", val: "bad", want: 0},
		{name: "missing", val: nil, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{}
			if tc.val != nil {
				m["k"] = tc.val
			}
			got := getInt(m, "k")
			if got != tc.want {
				t.Fatalf("getInt() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMergeUserEventsPrefersHigherPoint(t *testing.T) {
	oldData := map[string]any{
		"userEvents": bson.A{
			map[string]any{"eventId": int64(1), "eventPoint": int64(100)},
		},
	}
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(1), "eventPoint": int64(120)},
			map[string]any{"eventId": int64(2), "eventPoint": int64(10)},
		},
	}

	merged := mergeUserEvents(oldData, newData)
	if len(merged) != 2 {
		t.Fatalf("mergeUserEvents() length = %d, want 2", len(merged))
	}

	points := map[int64]int64{}
	for _, item := range merged {
		ev, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("merged item has unexpected type %T", item)
		}
		points[getInt(ev, "eventId")] = getInt(ev, "eventPoint")
	}
	if points[1] != 120 {
		t.Fatalf("eventId=1 point = %d, want 120", points[1])
	}
	if points[2] != 10 {
		t.Fatalf("eventId=2 point = %d, want 10", points[2])
	}
}

func TestMergeWorldBloomsPrefersHigherChapterPoint(t *testing.T) {
	oldData := map[string]any{
		"userWorldBlooms": bson.A{
			map[string]any{"eventId": int64(3), "gameCharacterId": int64(5), "worldBloomChapterPoint": int64(50)},
		},
	}
	newData := map[string]any{
		"userWorldBlooms": []any{
			map[string]any{"eventId": int64(3), "gameCharacterId": int64(5), "worldBloomChapterPoint": int64(80)},
			map[string]any{"eventId": int64(9), "gameCharacterId": int64(1), "worldBloomChapterPoint": int64(2)},
		},
	}

	merged := mergeWorldBlooms(oldData, newData)
	if len(merged) != 2 {
		t.Fatalf("mergeWorldBlooms() length = %d, want 2", len(merged))
	}

	points := map[string]int64{}
	for _, item := range merged {
		bloom, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("merged bloom has unexpected type %T", item)
		}
		key := fmt.Sprintf("%d:%d", getInt(bloom, "eventId"), getInt(bloom, "gameCharacterId"))
		points[key] = getInt(bloom, "worldBloomChapterPoint")
	}

	if points["3:5"] != 80 {
		t.Fatalf("expected merged chapter point to keep latest value")
	}
}

func TestBuildFinalDataMergesAndKeepsOtherKeys(t *testing.T) {
	m := &MongoDBManager{}
	oldData := map[string]any{
		"userEvents": bson.A{
			map[string]any{"eventId": int64(1), "eventPoint": int64(100)},
		},
		"userGachas": bson.A{
			map[string]any{"gachaId": int64(3), "gachaBehaviorId": int64(7), "lastSpinAt": int64(1000)},
		},
	}
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(1), "eventPoint": int64(200)},
		},
		"userGachas": []any{
			map[string]any{"gachaId": int64(3), "gachaBehaviorId": int64(7), "lastSpinAt": int64(2000)},
		},
		"plain": "value",
	}

	finalData := m.buildFinalData(oldData, newData)

	if finalData["plain"] != "value" {
		t.Fatalf("buildFinalData() plain = %v, want value", finalData["plain"])
	}
	events, ok := finalData["userEvents"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("buildFinalData() userEvents malformed: %T, len=%d", finalData["userEvents"], len(events))
	}
	ev, ok := events[0].(map[string]any)
	if !ok {
		t.Fatalf("event type = %T, want map[string]any", events[0])
	}
	if getInt(ev, "eventPoint") != 200 {
		t.Fatalf("eventPoint = %d, want 200", getInt(ev, "eventPoint"))
	}
	gachas, ok := finalData["userGachas"].([]any)
	if !ok || len(gachas) != 1 {
		t.Fatalf("buildFinalData() userGachas malformed: %T, len=%d", finalData["userGachas"], len(gachas))
	}
	gacha, ok := gachas[0].(map[string]any)
	if !ok {
		t.Fatalf("gacha type = %T, want map[string]any", gachas[0])
	}
	if getInt(gacha, "lastSpinAt") != 2000 {
		t.Fatalf("lastSpinAt = %d, want 2000", getInt(gacha, "lastSpinAt"))
	}
}

func TestMergeUserEventsSupportsMongoDecodedDocumentTypes(t *testing.T) {
	oldData := map[string]any{
		"userEvents": bson.A{
			bson.D{
				{Key: "eventId", Value: int64(1)},
				{Key: "eventPoint", Value: int64(100)},
			},
		},
	}
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(2), "eventPoint": int64(10)},
		},
	}

	merged := mergeUserEvents(oldData, newData)
	if len(merged) != 2 {
		t.Fatalf("mergeUserEvents() length = %d, want 2", len(merged))
	}

	points := map[int64]int64{}
	for _, item := range merged {
		ev, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("merged item has unexpected type %T", item)
		}
		points[getInt(ev, "eventId")] = getInt(ev, "eventPoint")
	}
	if points[1] != 100 {
		t.Fatalf("eventId=1 point = %d, want 100", points[1])
	}
	if points[2] != 10 {
		t.Fatalf("eventId=2 point = %d, want 10", points[2])
	}
}

func TestMergeWorldBloomsSupportsMongoDecodedDocumentTypes(t *testing.T) {
	oldData := map[string]any{
		"userWorldBlooms": bson.A{
			bson.D{
				{Key: "eventId", Value: int64(3)},
				{Key: "gameCharacterId", Value: int64(5)},
				{Key: "worldBloomChapterPoint", Value: int64(50)},
			},
		},
	}
	newData := map[string]any{
		"userWorldBlooms": []any{
			map[string]any{"eventId": int64(9), "gameCharacterId": int64(1), "worldBloomChapterPoint": int64(2)},
		},
	}

	merged := mergeWorldBlooms(oldData, newData)
	if len(merged) != 2 {
		t.Fatalf("mergeWorldBlooms() length = %d, want 2", len(merged))
	}

	points := map[string]int64{}
	for _, item := range merged {
		bloom, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("merged bloom has unexpected type %T", item)
		}
		key := fmt.Sprintf("%d:%d", getInt(bloom, "eventId"), getInt(bloom, "gameCharacterId"))
		points[key] = getInt(bloom, "worldBloomChapterPoint")
	}

	if points["3:5"] != 50 {
		t.Fatalf("eventId=3 gameCharacterId=5 point = %d, want 50", points["3:5"])
	}
	if points["9:1"] != 2 {
		t.Fatalf("eventId=9 gameCharacterId=1 point = %d, want 2", points["9:1"])
	}
}

func TestMergeUserGachasPrefersLaterLastSpinAt(t *testing.T) {
	oldData := map[string]any{
		"userGachas": bson.A{
			map[string]any{"gachaId": int64(1), "gachaBehaviorId": int64(10), "lastSpinAt": int64(1000)},
		},
	}
	newData := map[string]any{
		"userGachas": []any{
			map[string]any{"gachaId": int64(1), "gachaBehaviorId": int64(10), "lastSpinAt": int64(2000)},
			map[string]any{"gachaId": int64(2), "gachaBehaviorId": int64(20), "lastSpinAt": int64(500)},
		},
	}

	merged := mergeUserGachas(oldData, newData)
	if len(merged) != 2 {
		t.Fatalf("mergeUserGachas() length = %d, want 2", len(merged))
	}

	points := map[string]int64{}
	for _, item := range merged {
		gacha, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("merged gacha has unexpected type %T", item)
		}
		key := fmt.Sprintf("%d:%d", getInt(gacha, "gachaId"), getInt(gacha, "gachaBehaviorId"))
		points[key] = getInt(gacha, "lastSpinAt")
	}

	if points["1:10"] != 2000 {
		t.Fatalf("gachaId=1 gachaBehaviorId=10 lastSpinAt = %d, want 2000", points["1:10"])
	}
	if points["2:20"] != 500 {
		t.Fatalf("gachaId=2 gachaBehaviorId=20 lastSpinAt = %d, want 500", points["2:20"])
	}
}

func TestMergeUserGachasSupportsMongoDecodedDocumentTypes(t *testing.T) {
	oldData := map[string]any{
		"userGachas": bson.A{
			bson.D{
				{Key: "gachaId", Value: int64(1)},
				{Key: "gachaBehaviorId", Value: int64(10)},
				{Key: "lastSpinAt", Value: int64(1000)},
			},
		},
	}
	newData := map[string]any{
		"userGachas": []any{
			map[string]any{"gachaId": int64(2), "gachaBehaviorId": int64(20), "lastSpinAt": int64(500)},
		},
	}

	merged := mergeUserGachas(oldData, newData)
	if len(merged) != 2 {
		t.Fatalf("mergeUserGachas() length = %d, want 2", len(merged))
	}

	points := map[string]int64{}
	for _, item := range merged {
		gacha, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("merged gacha has unexpected type %T", item)
		}
		key := fmt.Sprintf("%d:%d", getInt(gacha, "gachaId"), getInt(gacha, "gachaBehaviorId"))
		points[key] = getInt(gacha, "lastSpinAt")
	}

	if points["1:10"] != 1000 {
		t.Fatalf("gachaId=1 gachaBehaviorId=10 lastSpinAt = %d, want 1000", points["1:10"])
	}
	if points["2:20"] != 500 {
		t.Fatalf("gachaId=2 gachaBehaviorId=20 lastSpinAt = %d, want 500", points["2:20"])
	}
}

func TestMergeUserEventsPrefersNewDataWithRank(t *testing.T) {
	// Old data has same eventPoint but no rank
	oldData := map[string]any{
		"userEvents": bson.A{
			map[string]any{"eventId": int64(199), "eventPoint": int64(1500000)},
		},
	}
	// New data has same eventPoint but has rank data (post-event) - should replace
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(199), "eventPoint": int64(1500000), "rank": int64(127177)},
		},
	}

	merged := mergeUserEvents(oldData, newData)
	if len(merged) != 1 {
		t.Fatalf("mergeUserEvents() length = %d, want 1", len(merged))
	}

	ev, ok := merged[0].(map[string]any)
	if !ok {
		t.Fatalf("merged item has unexpected type %T", merged[0])
	}

	// Should have the new event with rank since eventPoints are equal
	if _, hasRank := ev["rank"]; !hasRank {
		t.Fatalf("merged event should have rank field")
	}
}

func TestMergeUserEventsKeepsHigherPointWithoutRank(t *testing.T) {
	// When neither has rank, prefer higher eventPoint
	oldData := map[string]any{
		"userEvents": bson.A{
			map[string]any{"eventId": int64(199), "eventPoint": int64(2000000)},
		},
	}
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(199), "eventPoint": int64(1500000)},
		},
	}

	merged := mergeUserEvents(oldData, newData)
	if len(merged) != 1 {
		t.Fatalf("mergeUserEvents() length = %d, want 1", len(merged))
	}

	ev, ok := merged[0].(map[string]any)
	if !ok {
		t.Fatalf("merged item has unexpected type %T", merged[0])
	}

	// Should keep the old event with higher point since neither has rank
	if getInt(ev, "eventPoint") != 2000000 {
		t.Fatalf("eventPoint = %d, want 2000000 (higher point)", getInt(ev, "eventPoint"))
	}
}

func TestMergeUserEventsHigherPointWinsOverRank(t *testing.T) {
	// Old data has rank but lower eventPoint
	oldData := map[string]any{
		"userEvents": bson.A{
			map[string]any{"eventId": int64(199), "eventPoint": int64(1000000), "rank": int64(200000)},
		},
	}
	// New data has higher eventPoint but no rank yet (event still ongoing)
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(199), "eventPoint": int64(1500000)},
		},
	}

	merged := mergeUserEvents(oldData, newData)
	if len(merged) != 1 {
		t.Fatalf("mergeUserEvents() length = %d, want 1", len(merged))
	}

	ev, ok := merged[0].(map[string]any)
	if !ok {
		t.Fatalf("merged item has unexpected type %T", merged[0])
	}

	// Higher eventPoint should win, even if old had rank
	if getInt(ev, "eventPoint") != 1500000 {
		t.Fatalf("eventPoint = %d, want 1500000 (higher point wins)", getInt(ev, "eventPoint"))
	}
}

func TestMergeUserEventsAddsNewEventIds(t *testing.T) {
	// Old data has event 199
	oldData := map[string]any{
		"userEvents": bson.A{
			map[string]any{"eventId": int64(199), "eventPoint": int64(1000000)},
		},
	}
	// New data has event 200 (new event that doesn't exist in old data)
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(200), "eventPoint": int64(50000)},
		},
	}

	merged := mergeUserEvents(oldData, newData)
	if len(merged) != 2 {
		t.Fatalf("mergeUserEvents() length = %d, want 2", len(merged))
	}

	points := map[int64]int64{}
	for _, item := range merged {
		ev, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("merged item has unexpected type %T", item)
		}
		points[getInt(ev, "eventId")] = getInt(ev, "eventPoint")
	}

	if points[199] != 1000000 {
		t.Fatalf("eventId=199 point = %d, want 1000000", points[199])
	}
	if points[200] != 50000 {
		t.Fatalf("eventId=200 point = %d, want 50000", points[200])
	}
}
