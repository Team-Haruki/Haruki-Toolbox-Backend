package gamedata

import (
	json "encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

var costumeKeys = []string{"userCostume3dStatuses", "userCostume3dShopItems"}

func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}
	return m
}

// The profile body is the full private document minus the omitted keys: same
// identity members, same extra splice, same userGamedata filter.
func TestPrivateBodyWithoutDropsOnlyTheOmittedKeys(t *testing.T) {
	stored := `{"userId":28808221489823746,"name":"n","deck":1,"exp":2,"totalExp":3,` +
		`"coin":4,"rank":5,"secretToken":"nope"}`
	r := newTestRow(t, catalog.Suite(), map[string]string{
		"userGamedata":           stored,
		"userCards":              `[{"cardId":1}]`,
		"userCostume3dStatuses":  `[{"costume3dId":1,"status":"have"}]`,
		"userCostume3dShopItems": `[{"costume3dShopItemId":2,"status":"bought"}]`,
	}, `{"userBrandNewKey":[{"id":7}],"compactUserCostume3dStatuses":{"__ENUM__":{}}}`)
	r.UploadTime, r.HasUpload = 1752600000, true

	full, err := r.PrivateBody(nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := r.PrivateBodyWithout(costumeKeys)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeObject(t, body)
	want := decodeObject(t, full)
	delete(want, "userCostume3dStatuses")
	delete(want, "userCostume3dShopItems")
	// The parked compact spelling of an omitted key must not come back
	// through the extra splice.
	delete(want, "compactUserCostume3dStatuses")

	gotKeys := mapKeys(got)
	wantKeys := mapKeys(want)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("keys\n got %v\nwant %v", gotKeys, wantKeys)
	}
	for _, k := range []string{"_id", "_idString", "server", "upload_time", "userCards", "userBrandNewKey"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("%s missing from profile body: %s", k, body)
		}
	}
	ugd, ok := got["userGamedata"].(map[string]any)
	if !ok {
		t.Fatalf("userGamedata missing: %s", body)
	}
	if _, leaked := ugd["secretToken"]; leaked {
		t.Fatalf("userGamedata not filtered: %s", body)
	}
	if _, ok := ugd["userIdString"]; !ok {
		t.Fatalf("nested userGamedata lost userIdString: %s", body)
	}
	if !strings.Contains(string(body), "28808221489823746") {
		t.Fatalf("userId lost precision: %s", body)
	}
}

// A key the row does not carry stays omitted, never null and never [].
func TestPrivateBodyWithoutOmitsAbsentKeys(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[]`}, "")
	body, err := r.PrivateBodyWithout(costumeKeys)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeObject(t, body)
	for _, k := range []string{"userDecks", "userGamedata"} {
		if _, present := got[k]; present {
			t.Fatalf("absent key %s rendered: %s", k, body)
		}
	}
}

// With nothing omitted the profile render is byte-identical to the full body.
func TestPrivateBodyWithoutNothingIsTheFullBody(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{
		"userCards":             `[{"cardId":1}]`,
		"userCostume3dStatuses": `[{"costume3dId":1}]`,
	}, `{"userBrandNewKey":1}`)
	full, err := r.PrivateBody(nil)
	if err != nil {
		t.Fatal(err)
	}
	same, err := r.PrivateBodyWithout(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(full) != string(same) {
		t.Fatalf("\n got %s\nwant %s", same, full)
	}
}

// Omitting a key that names no column still drops it from the extra splice.
func TestPrivateBodyWithoutDropsUncataloguedExtraKey(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[]`}, `{"userBrandNewKey":1,"userOther":2}`)
	body, err := r.PrivateBodyWithout([]string{"userBrandNewKey"})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeObject(t, body)
	if _, present := got["userBrandNewKey"]; present {
		t.Fatalf("omitted extra key rendered: %s", body)
	}
	if _, present := got["userOther"]; !present {
		t.Fatalf("other extra key dropped: %s", body)
	}
}

// The profile read selects every column except the omitted ones, plus extra,
// in pinned order, so one omit list always maps to one prepared statement.
func TestSelectWithoutSkipsOmittedColumns(t *testing.T) {
	s := NewStore(nil, catalog.Suite())
	quoted, plain := s.selectWithout(costumeKeys)
	if len(quoted) != len(plain) {
		t.Fatalf("quoted %d != plain %d", len(quoted), len(plain))
	}
	if want := catalog.Suite().Len() - 2 + 1; len(plain) != want {
		t.Fatalf("selected %d columns, want %d", len(plain), want)
	}
	for _, col := range []string{"user_costume3d_statuses_j", "user_costume3d_shop_items_j"} {
		if slices.Contains(plain, col) {
			t.Fatalf("%s selected", col)
		}
	}
	if plain[len(plain)-1] != catalog.ExtraColumn {
		t.Fatalf("extra not selected last: %v", plain[len(plain)-3:])
	}
	again, _ := s.selectWithout([]string{"userCostume3dShopItems", "compactUserCostume3dStatuses"})
	if !slices.Equal(again, quoted) {
		t.Fatal("alias spelling or order changed the statement")
	}
}

func TestFetchWithoutRejectsNilStoreAndUnknownRegion(t *testing.T) {
	var nilStore *Store
	if _, err := nilStore.FetchWithout(t.Context(), 1, "jp", nil); err == nil {
		t.Fatal("nil store did not error")
	}
	s := &Store{pool: &Pool{}, cat: catalog.Suite()}
	if _, err := s.FetchWithout(t.Context(), 1, "xx", nil); err != ErrNoRow {
		t.Fatalf("unknown region: err = %v, want ErrNoRow", err)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
