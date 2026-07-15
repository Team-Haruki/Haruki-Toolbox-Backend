package userprivateapi

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBuildKeyProjection(t *testing.T) {
	cases := []struct {
		name       string
		requestKey string
		want       bson.M
	}{
		{"no key returns nil (full document)", "", nil},
		{"single key", "userCards", bson.M{"userCards": 1}},
		{"multiple keys", "userCards,userAreas", bson.M{"userCards": 1, "userAreas": 1}},
		{"trims whitespace", " userCards , userAreas ", bson.M{"userCards": 1, "userAreas": 1}},
		{"skips empty segments", "userCards,,userAreas", bson.M{"userCards": 1, "userAreas": 1}},
		{"all-empty segments returns nil", " , , ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildKeyProjection(tc.requestKey)
			if len(got) != len(tc.want) {
				t.Fatalf("buildKeyProjection(%q) = %v, want %v", tc.requestKey, got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("buildKeyProjection(%q)[%q] = %v, want %v", tc.requestKey, k, got[k], v)
				}
			}
		})
	}
}

// TestBuildKeyProjectionNeverExcludesID is a regression guard: excluding _id makes
// Mongo return an empty document when a request asks only for fields the document
// does not contain, which the caller's len()==0 check would misreport as a 404 for a
// live account. A projection must therefore never set _id to 0.
func TestBuildKeyProjectionNeverExcludesID(t *testing.T) {
	for _, requestKey := range []string{"userCards", "a,b,c", " userDecks ", "missingField"} {
		projection := buildKeyProjection(requestKey)
		if projection == nil {
			t.Fatalf("buildKeyProjection(%q) unexpectedly returned nil", requestKey)
		}
		if v, ok := projection["_id"]; ok {
			t.Fatalf("buildKeyProjection(%q) must not project _id (got _id=%v); excluding it breaks the exists/not-found signal", requestKey, v)
		}
	}
}
