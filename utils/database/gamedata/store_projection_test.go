package gamedata

import (
	"slices"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

func TestProjectionCanonicalizesColumnsWithoutChangingRequestedKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		cat  *catalog.Catalog
		keys []string
	}{
		{"suite", catalog.Suite(), []string{"userCards", "compactUserMusicResults", "userMusicResults", "futureKey", "server", "userDecks"}},
		{"mysekai", catalog.Mysekai(), []string{"updatedResources.userMysekaiPhotos", "updatedResources", "isEnabled", "futureKey", "_id"}},
		{"metadata", catalog.Suite(), []string{"upload_time", "_id", "server"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore(nil, tc.cat)
			original := slices.Clone(tc.keys)
			quoted, plain := s.selectFor(tc.keys)
			if !slices.Equal(original, tc.keys) {
				t.Fatal("request order changed")
			}
			if !slices.IsSorted(plain) {
				t.Fatalf("unordered columns: %v", plain)
			}
			for i, col := range plain {
				if quoted[i] != catalog.QuoteIdent(col) {
					t.Fatalf("scan mapping differs at %d", i)
				}
				if i > 0 && col == plain[i-1] {
					t.Fatalf("duplicate column %s", col)
				}
			}
			slices.Reverse(original)
			q2, p2 := s.selectFor(original)
			if !slices.Equal(quoted, q2) || !slices.Equal(plain, p2) {
				t.Fatal("same field set generated different projection")
			}
		})
	}
}

// Execute sorted projections against PostgreSQL to catch scan-position errors
// that otherwise silently assign a valid JSON value to the wrong field.
func TestFetchCanonicalProjectionPreservesFieldMapping(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Mysekai())
	const id = int64(28808221489823746)
	mustWrite(t, s, ctx, id, "jp", map[string]any{
		"isEnabled": true, "policy": "public", "futureKey": map[string]any{"id": 2},
		"updatedResources": map[string]any{"userMysekaiPhotos": []any{map[string]any{"id": 3}}, "futureChild": []any{4}},
	}, WriteMysekai)
	want := map[string]string{"isEnabled": "true", "policy": `"public"`, "futureKey": `{"id":2}`, "updatedResources.userMysekaiPhotos": `[{"id":3}]`}
	keys := []string{"policy", "updatedResources", "futureKey", "isEnabled", "updatedResources.userMysekaiPhotos"}
	for range 2 {
		row, err := s.Fetch(ctx, id, "jp", keys)
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range want {
			got, ok, err := row.RawValue(key)
			if err != nil || !ok || string(got) != value {
				t.Fatalf("%s = %s,%v,%v want %s", key, got, ok, err, value)
			}
		}
		parent, ok, err := row.RawValue("updatedResources")
		if err != nil || !ok || !strings.Contains(string(parent), `"futureChild":[4]`) {
			t.Fatalf("parent lost extra: %s,%v,%v", parent, ok, err)
		}
		slices.Reverse(keys)
	}
}
