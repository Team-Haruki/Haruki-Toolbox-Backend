package suiterestore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const testFixture = `{
  "userActionSets": ["id", "status"],
  "userHonors": ["honorId", "level", "obtainedAt"],
  "userStamps": ["stampId", "obtainedAt"],
  "userCards": [
    "cardId", "level", "exp", "totalExp", "skillLevel", "skillExp",
    "totalSkillExp", "masterRank", "specialTrainingStatus", "defaultImage",
    "duplicateCount", "createdAt",
    ["episodes", ["cardEpisodeId", "scenarioStatus", "scenarioStatusReasons", "isNotSkipped"]]
  ],
  "userShops": ["shopId", ["userShopItems", ["shopItemId", "level", "status"]]],
  "userVirtualShops": [
    "virtualShopId",
    ["userVirtualShopItems", ["virtualShopId", "virtualShopItemId", "status", "buyCount"]]
  ]
}`

func createTestRestorer(t *testing.T) *Restorer {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "suite_structures.json")
	if err := os.WriteFile(path, []byte(testFixture), 0644); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}
	r, err := NewFromFile(path)
	if err != nil {
		t.Fatalf("NewFromFile: %v", err)
	}
	return r
}

func TestRestoreFields_TopLevel_ArrayToDict(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{
		"userActionSets": []any{
			[]any{1, "active"},
			[]any{2, "inactive"},
		},
		"userHonors": []any{
			[]any{100, 5, "2024-01-01"},
		},
	}

	result := r.RestoreFields(data)

	actionSets := result["userActionSets"].([]any)
	if len(actionSets) != 2 {
		t.Fatalf("expected 2 action sets, got %d", len(actionSets))
	}
	first := actionSets[0].(map[string]any)
	if first["id"] != 1 || first["status"] != "active" {
		t.Errorf("unexpected first action set: %v", first)
	}

	honors := result["userHonors"].([]any)
	honor := honors[0].(map[string]any)
	if honor["honorId"] != 100 || honor["level"] != 5 || honor["obtainedAt"] != "2024-01-01" {
		t.Errorf("unexpected honor: %v", honor)
	}
}

func TestRestoreFields_AlreadyDict(t *testing.T) {
	r := createTestRestorer(t)
	original := map[string]any{"id": 1, "status": "active"}
	data := map[string]any{
		"userActionSets": []any{original},
	}

	result := r.RestoreFields(data)
	restored := result["userActionSets"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(restored, original) {
		t.Errorf("dict data should not be modified, got %v", restored)
	}
}

func TestRestoreFields_MixedArrayAndDict(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{
		"userStamps": []any{
			[]any{1, "2024-01-01"},
			map[string]any{"stampId": 2, "obtainedAt": "2024-02-01"},
		},
	}

	result := r.RestoreFields(data)
	stamps := result["userStamps"].([]any)
	first := stamps[0].(map[string]any)
	if first["stampId"] != 1 {
		t.Errorf("expected stampId=1, got %v", first["stampId"])
	}
	second := stamps[1].(map[string]any)
	if second["stampId"] != 2 {
		t.Errorf("expected stampId=2, got %v", second["stampId"])
	}
}

func TestRestoreFields_NestedArray_UserCards_Episodes(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{
		"userCards": []any{
			// Card with episodes as nested arrays
			[]any{
				1, 60, 1000, 5000, 4, 200, 800, 3, "done", "special", 2, "2024-01-01",
				[]any{
					[]any{101, "read", "reason1", true},
					[]any{102, "unread", nil, false},
				},
			},
		},
	}

	result := r.RestoreFields(data)
	cards := result["userCards"].([]any)
	card := cards[0].(map[string]any)

	if card["cardId"] != 1 || card["level"] != 60 || card["masterRank"] != 3 {
		t.Errorf("card fields wrong: %v", card)
	}

	episodes := card["episodes"].([]any)
	if len(episodes) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(episodes))
	}
	ep1 := episodes[0].(map[string]any)
	if ep1["cardEpisodeId"] != 101 || ep1["scenarioStatus"] != "read" {
		t.Errorf("unexpected episode 1: %v", ep1)
	}
	ep2 := episodes[1].(map[string]any)
	if ep2["cardEpisodeId"] != 102 {
		t.Errorf("unexpected episode 2: %v", ep2)
	}
	// nil scenarioStatusReasons should not create key
	if _, exists := ep2["scenarioStatusReasons"]; exists {
		t.Errorf("nil value should not create key")
	}
}

func TestRestoreFields_NestedArray_UserShops(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{
		"userShops": []any{
			[]any{
				10,
				[]any{
					[]any{201, 3, "bought"},
					[]any{202, 1, "available"},
				},
			},
		},
	}

	result := r.RestoreFields(data)
	shops := result["userShops"].([]any)
	shop := shops[0].(map[string]any)
	if shop["shopId"] != 10 {
		t.Errorf("expected shopId=10, got %v", shop["shopId"])
	}
	items := shop["userShopItems"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	item1 := items[0].(map[string]any)
	if item1["shopItemId"] != 201 || item1["level"] != 3 || item1["status"] != "bought" {
		t.Errorf("unexpected shop item 1: %v", item1)
	}
}

func TestRestoreFields_NestedAlreadyDict(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{
		"userShops": []any{
			map[string]any{
				"shopId": 10,
				"userShopItems": []any{
					[]any{201, 3, "bought"}, // still array
				},
			},
		},
	}

	result := r.RestoreFields(data)
	shops := result["userShops"].([]any)
	shop := shops[0].(map[string]any)
	items := shop["userShopItems"].([]any)
	item := items[0].(map[string]any)
	if item["shopItemId"] != 201 {
		t.Errorf("nested restore in already-dict parent failed: %v", item)
	}
}

func TestRestoreFields_EmptyData(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{}
	result := r.RestoreFields(data)
	if len(result) != 0 {
		t.Errorf("empty data should remain empty, got %v", result)
	}
}

func TestRestoreFields_NilValues(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{"userCards": nil}
	result := r.RestoreFields(data)
	if result["userCards"] != nil {
		t.Errorf("nil value should remain nil")
	}
}

func TestRestoreFields_ArrayWithNilElements(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{
		"userHonors": []any{
			[]any{100, nil, "2024-01-01"},
		},
	}
	result := r.RestoreFields(data)
	honor := result["userHonors"].([]any)[0].(map[string]any)
	if honor["honorId"] != 100 {
		t.Errorf("expected honorId=100, got %v", honor["honorId"])
	}
	if _, exists := honor["level"]; exists {
		t.Errorf("nil value should not create key")
	}
	if honor["obtainedAt"] != "2024-01-01" {
		t.Errorf("expected obtainedAt='2024-01-01', got %v", honor["obtainedAt"])
	}
}

func TestRestoreFields_UnknownFields_Untouched(t *testing.T) {
	r := createTestRestorer(t)
	data := map[string]any{
		"userGamedata":   map[string]any{"userId": 123},
		"unknownField":   []any{1, 2, 3},
		"userActionSets": []any{[]any{1, "active"}},
	}
	result := r.RestoreFields(data)
	if !reflect.DeepEqual(result["unknownField"], []any{1, 2, 3}) {
		t.Errorf("unknown field should not be modified")
	}
	sets := result["userActionSets"].([]any)
	first := sets[0].(map[string]any)
	if first["id"] != 1 {
		t.Errorf("userActionSets should be restored")
	}
}

func TestNewFromFile_InvalidPath(t *testing.T) {
	_, err := NewFromFile("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestNewFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("{invalid}"), 0644)
	_, err := NewFromFile(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
