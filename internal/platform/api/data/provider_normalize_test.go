package data

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNormalizeProviderResponseAddsGameUserIDString(t *testing.T) {
	t.Parallel()

	const userID int64 = 9223372036854775000
	got, ok := NormalizeProviderResponse(bson.D{
		{Key: "userGamedata", Value: bson.D{
			{Key: "userId", Value: userID},
			{Key: "rank", Value: int32(123)},
		}},
		{Key: "userCards", Value: bson.A{bson.D{{Key: "cardId", Value: int32(1)}}}},
	}).(map[string]any)
	if !ok {
		t.Fatalf("NormalizeProviderResponse result = %T, want map", got)
	}
	userGamedata, ok := got["userGamedata"].(map[string]any)
	if !ok {
		t.Fatalf("userGamedata = %T, want map", got["userGamedata"])
	}
	if userGamedata["userId"] != userID {
		t.Fatalf("userId = %v, want %d", userGamedata["userId"], userID)
	}
	if userGamedata["userIdString"] != "9223372036854775000" {
		t.Fatalf("userIdString = %v", userGamedata["userIdString"])
	}
	if _, exists := userGamedata["rankString"]; exists {
		t.Fatalf("rankString should not be added")
	}
}

func TestNormalizeProviderResponseAddsTopLevelIDString(t *testing.T) {
	t.Parallel()

	const userID int64 = 9223372036854775000
	got, ok := NormalizeProviderResponse(bson.D{
		{Key: "_id", Value: userID},
		{Key: "server", Value: "jp"},
	}).(map[string]any)
	if !ok {
		t.Fatalf("NormalizeProviderResponse result = %T, want map", got)
	}
	if got["_id"] != userID {
		t.Fatalf("_id = %v, want %d", got["_id"], userID)
	}
	if got["_idString"] != "9223372036854775000" {
		t.Fatalf("_idString = %v", got["_idString"])
	}
}

func TestNormalizeProviderResponseRestoredCompactRows(t *testing.T) {
	t.Parallel()

	const userID int64 = 9223372036854775000
	rows := []bson.D{
		{
			{Key: "userGamedata", Value: bson.D{{Key: "userId", Value: userID}}},
			{Key: "score", Value: int32(10)},
		},
	}
	got, ok := NormalizeProviderResponse(rows).([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("NormalizeProviderResponse rows = %#v", got)
	}
	row, ok := got[0].(map[string]any)
	if !ok {
		t.Fatalf("row = %T, want map", got[0])
	}
	userGamedata, ok := row["userGamedata"].(map[string]any)
	if !ok {
		t.Fatalf("userGamedata = %T, want map", row["userGamedata"])
	}
	if userGamedata["userIdString"] != "9223372036854775000" {
		t.Fatalf("nested userIdString = %v", userGamedata["userIdString"])
	}
	if _, exists := row["scoreString"]; exists {
		t.Fatalf("scoreString should not be added")
	}
}
