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

func valueFromD(d bson.D, key string) any {
	for _, elem := range d {
		if elem.Key == key {
			return elem.Value
		}
	}
	return nil
}
