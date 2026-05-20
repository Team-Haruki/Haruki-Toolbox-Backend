package data

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCompactFieldName(t *testing.T) {
	if got := compactFieldName("userDecks"); got != "compactUserDecks" {
		t.Fatalf("compactFieldName() = %q, want %q", got, "compactUserDecks")
	}
	if got := compactFieldName(""); got != "" {
		t.Fatalf("compactFieldName(empty) = %q, want empty", got)
	}
}

func TestBuildSuiteProjection(t *testing.T) {
	proj := buildSuiteProjection([]string{"userDecks", "userGamedata"})
	if proj["_id"] != 0 {
		t.Fatalf("projection _id should be 0, got %v", proj["_id"])
	}
	if proj["userDecks"] != 1 {
		t.Fatalf("projection should include userDecks")
	}
	if proj["compactUserDecks"] != 1 {
		t.Fatalf("projection should include compactUserDecks")
	}
	for _, field := range userGamedataAllowedFields {
		key := "userGamedata." + field
		if proj[key] != 1 {
			t.Fatalf("projection missing %s", key)
		}
	}
}

func TestBuildMysekaiProjection(t *testing.T) {
	empty := buildMysekaiProjection(nil)
	if empty["_id"] != 0 || empty["server"] != 0 {
		t.Fatalf("default mysekai projection should hide _id and server")
	}
	withKeys := buildMysekaiProjection([]string{"updatedResources"})
	if withKeys["_id"] != 0 {
		t.Fatalf("mysekai projection should hide _id")
	}
	if withKeys["updatedResources"] != 1 {
		t.Fatalf("mysekai projection should include requested key")
	}
}

func TestGetValueFromResultDirect(t *testing.T) {
	result := bson.D{{Key: "userDecks", Value: bson.A{1, 2, 3}}}
	val := GetValueFromResult(result, "userDecks")
	arr, ok := val.(bson.A)
	if !ok {
		t.Fatalf("expected bson.A, got %T", val)
	}
	if len(arr) != 3 {
		t.Fatalf("array length = %d, want 3", len(arr))
	}
}

func TestGetValueFromResultCompactRestore(t *testing.T) {
	result := bson.D{
		{
			Key: "compactUserDecks",
			Value: bson.D{
				{Key: "id", Value: bson.A{int32(1), int32(2)}},
				{Key: "name", Value: bson.A{int32(0), int32(1)}},
				{
					Key: "__ENUM__",
					Value: bson.D{
						{Key: "name", Value: bson.A{"first", "second"}},
					},
				},
			},
		},
	}

	val := GetValueFromResult(result, "userDecks")
	rows, ok := val.([]bson.D)
	if !ok {
		t.Fatalf("expected []bson.D, got %T", val)
	}
	if len(rows) != 2 {
		t.Fatalf("restored row length = %d, want 2", len(rows))
	}

	first := rows[0]
	firstID, ok := valueFromD(first, "id").(int32)
	if !ok || firstID != 1 {
		t.Fatalf("first row id = %v, want 1", valueFromD(first, "id"))
	}
	firstName, ok := valueFromD(first, "name").(string)
	if !ok || firstName != "first" {
		t.Fatalf("first row name = %v, want first", valueFromD(first, "name"))
	}
}

func TestGetValueFromResultMissingKey(t *testing.T) {
	val := GetValueFromResult(bson.D{}, "missing")
	arr, ok := val.(bson.A)
	if !ok {
		t.Fatalf("expected bson.A for missing key, got %T", val)
	}
	if len(arr) != 0 {
		t.Fatalf("missing key should return empty array, got len=%d", len(arr))
	}
}

func TestBSONDToMapNormalizesNestedValues(t *testing.T) {
	input := bson.D{
		{Key: "userGamedata", Value: bson.D{{Key: "userId", Value: int64(123)}}},
		{Key: "userCards", Value: []bson.D{
			{{Key: "cardId", Value: int32(1)}},
			{{Key: "cardId", Value: int32(2)}},
		}},
		{Key: "userHonors", Value: bson.A{bson.D{{Key: "honorId", Value: int32(10)}}}},
	}

	got := BSONDToMap(input)
	userGamedata, ok := got["userGamedata"].(map[string]any)
	if !ok {
		t.Fatalf("userGamedata should normalize to map, got %T", got["userGamedata"])
	}
	if userGamedata["userId"] != int64(123) {
		t.Fatalf("userId = %v, want 123", userGamedata["userId"])
	}

	userCards, ok := got["userCards"].([]any)
	if !ok || len(userCards) != 2 {
		t.Fatalf("userCards should normalize to []any length 2, got %#v", got["userCards"])
	}
	firstCard, ok := userCards[0].(map[string]any)
	if !ok || firstCard["cardId"] != int32(1) {
		t.Fatalf("first card = %#v, want cardId 1", userCards[0])
	}

	userHonors, ok := got["userHonors"].([]any)
	if !ok || len(userHonors) != 1 {
		t.Fatalf("userHonors should normalize to []any length 1, got %#v", got["userHonors"])
	}
	firstHonor, ok := userHonors[0].(map[string]any)
	if !ok || firstHonor["honorId"] != int32(10) {
		t.Fatalf("first honor = %#v, want honorId 10", userHonors[0])
	}
}

func TestDeckRecommendKeySets(t *testing.T) {
	suiteProjection := buildSuiteProjection(DeckRecommendSuiteKeys)
	for _, key := range []string{"userCards", "userAreas", "userCharacters", "userHonors"} {
		if suiteProjection[key] != 1 {
			t.Fatalf("suite projection missing %s", key)
		}
	}
	if suiteProjection["userGamedata.userId"] != 1 {
		t.Fatalf("suite projection should include allowed userGamedata fields")
	}

	mysekaiProjection := buildMysekaiProjection(DeckRecommendMysekaiKeys)
	for _, key := range DeckRecommendMysekaiKeys {
		if mysekaiProjection[key] != 1 {
			t.Fatalf("mysekai projection missing %s", key)
		}
	}
}

func valueFromD(d bson.D, key string) any {
	for _, elem := range d {
		if elem.Key == key {
			return elem.Value
		}
	}
	return nil
}
