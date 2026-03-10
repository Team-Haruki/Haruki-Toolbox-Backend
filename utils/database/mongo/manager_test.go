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
	}
	newData := map[string]any{
		"userEvents": []any{
			map[string]any{"eventId": int64(1), "eventPoint": int64(200)},
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
}
