package gamedata

import (
	"encoding/json"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// newTestRow builds a Row without a database, keyed by catalog KEY for brevity.
func newTestRow(t *testing.T, c *catalog.Catalog, values map[string]string, extra string) *Row {
	t.Helper()
	r := &Row{UserID: 28808221489823746, Server: "jp", cat: c, byColumn: map[string][]byte{}}
	for k, v := range values {
		e, place := c.Resolve(k)
		if place != catalog.PlaceColumn {
			t.Fatalf("test key %q is not a column (%v)", k, place)
		}
		r.byColumn[e.Column] = []byte(v)
	}
	if extra != "" {
		r.extra = []byte(extra)
	}
	return r
}

// --- suite shaping -----------------------------------------------------------

// A requested-but-absent suite key renders [], never omitted and never null.
func TestSuiteMissingKeyRendersEmptyArray(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[{"cardId":1}]`}, "")
	body, err := r.SuiteBody([]string{"userCards", "userDecks"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"userCards":[{"cardId":1}],"userDecks":[]}`
	if string(body) != want {
		t.Fatalf("\n got %s\nwant %s", body, want)
	}
}

// The single-key unwrap applies ONLY to an explicit ?key=. With no ?key= the
// keys come from the allowlist, and a one-entry allowlist must still be an
// object — otherwise a config edit silently changes the response shape.
func TestSuiteSingleKeyUnwrapRequiresAnExplicitRequestKey(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[{"cardId":1}]`}, "")

	bare, err := r.SuiteBody([]string{"userCards"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(bare) != `[{"cardId":1}]` {
		t.Fatalf("explicit single key did not unwrap: %s", bare)
	}

	wrapped, err := r.SuiteBody([]string{"userCards"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(wrapped) != `{"userCards":[{"cardId":1}]}` {
		t.Fatalf("allowlist-derived single key was unwrapped: %s", wrapped)
	}
}

func TestSuiteBareValueForMissingKeyIsEmptyArray(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), nil, "")
	body, err := r.SuiteBody([]string{"userCards"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `[]` {
		t.Fatalf("got %s", body)
	}
}

// userGamedata is asymmetric on purpose: absent means OMITTED in object shape
// but {} in bare shape. Both come from buildSuiteResponse / HandleSuiteRequest.
func TestUserGamedataAbsentIsOmittedInObjectButEmptyObjectBare(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[]`}, "")

	obj, err := r.SuiteBody([]string{"userCards", "userGamedata"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(obj) != `{"userCards":[]}` {
		t.Fatalf("absent userGamedata was not omitted: %s", obj)
	}

	bare, err := r.SuiteBody([]string{"userGamedata"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(bare) != `{}` {
		t.Fatalf("absent userGamedata bare value = %s, want {}", bare)
	}
}

// Only the seven allowlisted fields of userGamedata are ever served.
func TestUserGamedataIsFilteredToSevenFields(t *testing.T) {
	stored := `{"userId":28808221489823746,"name":"n","deck":1,"exp":2,"totalExp":3,` +
		`"coin":4,"rank":5,"secretToken":"nope","registeredAt":123}`
	r := newTestRow(t, catalog.Suite(), map[string]string{"userGamedata": stored}, "")
	body, err := r.SuiteBody([]string{"userGamedata"}, true)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(userGamedataAllowedFields) {
		t.Fatalf("served %d fields, want %d: %s", len(got), len(userGamedataAllowedFields), body)
	}
	for _, banned := range []string{"secretToken", "registeredAt"} {
		if _, leaked := got[banned]; leaked {
			t.Fatalf("%s leaked: %s", banned, body)
		}
	}
	// The id must survive as an exact literal, not through float64.
	if !containsSub(string(body), "28808221489823746") {
		t.Fatalf("userId lost precision: %s", body)
	}
}

// A compact-stored value must be expanded on the way out of the column.
func TestCompactColumnIsExpandedOnRead(t *testing.T) {
	compact := `{"__ENUM__":{"playResult":["clear"]},"musicId":[1],"playResult":[0]}`
	r := newTestRow(t, catalog.Suite(), map[string]string{"userMusicResults": compact}, "")
	body, err := r.SuiteBody([]string{"userMusicResults"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `[{"musicId":1,"playResult":"clear"}]` {
		t.Fatalf("got %s", body)
	}
}

// The same column holding row form must pass through untouched.
func TestRowFormColumnIsNotRewritten(t *testing.T) {
	rowForm := `[{"musicId":1,"playResult":"clear"}]`
	r := newTestRow(t, catalog.Suite(), map[string]string{"userMusicResults": rowForm}, "")
	body, err := r.SuiteBody([]string{"userMusicResults"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != rowForm {
		t.Fatalf("got %s", body)
	}
}

// A cn/tw upload arrives under compactUserX; the alias must serve the same
// column, expanded.
func TestCompactAliasServesTheSameColumn(t *testing.T) {
	compact := `{"__ENUM__":{"playResult":["clear"]},"musicId":[1],"playResult":[0]}`
	r := newTestRow(t, catalog.Suite(), map[string]string{"userMusicResults": compact}, "")
	body, err := r.SuiteBody([]string{"compactUserMusicResults"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `[{"musicId":1,"playResult":"clear"}]` {
		t.Fatalf("got %s", body)
	}
}

// --- mysekai shaping ---------------------------------------------------------

// mysekai never unwraps a single key and omits absent keys rather than
// rendering []. Both differ from suite and both are existing behaviour.
func TestMysekaiSingleKeyStaysAnObjectAndAbsentKeysAreOmitted(t *testing.T) {
	c := catalog.Mysekai()
	r := newTestRow(t, c, map[string]string{"updatedResources.userMysekaiPhotos": `[1]`}, "")
	body, err := r.MysekaiBody([]string{"updatedResources.userMysekaiPhotos", "isEnabled"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"updatedResources.userMysekaiPhotos":[1]}`
	if string(body) != want {
		t.Fatalf("\n got %s\nwant %s", body, want)
	}
}

// The flattened parent owns no column; requesting it must reassemble the
// children rather than serve nothing.
func TestFlattenParentIsReassembledFromItsChildren(t *testing.T) {
	c := catalog.Mysekai()
	r := newTestRow(t, c, map[string]string{
		"updatedResources.userMysekaiPhotos":      `[{"id":1}]`,
		"updatedResources.userMysekaiHarvestMaps": `[{"m":2}]`,
	}, "")
	body, err := r.wholeDocument(false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	sub, ok := got[c.FlattenKey].(map[string]any)
	if !ok {
		t.Fatalf("%s missing or not an object: %s", c.FlattenKey, body)
	}
	for _, k := range []string{"userMysekaiPhotos", "userMysekaiHarvestMaps"} {
		if _, ok := sub[k]; !ok {
			t.Fatalf("child %q missing from %s: %s", k, c.FlattenKey, body)
		}
	}
	if _, leaked := got["updatedResources.userMysekaiPhotos"]; leaked {
		t.Fatalf("flattened child leaked as a top-level key: %s", body)
	}
}

// --- private shaping ---------------------------------------------------------

// The private surface answers null, not [], for a key it cannot resolve — and a
// dotted path is exactly such a key, because it never descended.
func TestPrivateMissingKeyIsNullAndDottedStaysNull(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[1]`}, "")
	for _, k := range []string{"userDecks", "updatedResources.userMysekaiHarvestMaps"} {
		body, err := r.PrivateBody([]string{k})
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "null" {
			t.Fatalf("PrivateBody(%q) = %s, want null", k, body)
		}
	}
}

func TestPrivateWholeDocumentCarriesIdentity(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[1]`}, "")
	r.UploadTime, r.HasUpload = 1758686145, true
	body, err := r.PrivateBody(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	for _, k := range []string{"_id", "server", "upload_time", "userCards"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("private whole document missing %q: %s", k, body)
		}
	}
	if !containsSub(string(body), "28808221489823746") {
		t.Fatalf("_id lost precision: %s", body)
	}
}

// The public whole-document shape must NOT carry identity.
func TestPublicWholeDocumentOmitsIdentity(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[1]`}, "")
	body, err := r.wholeDocument(false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"_id", "server"} {
		if _, leaked := got[k]; leaked {
			t.Fatalf("%q leaked into a public body: %s", k, body)
		}
	}
}

// --- 404 rule ----------------------------------------------------------------

// The public faces 404 when EVERY requested key is absent, even though the row
// exists. A naive port answers 200 with a body full of [].
func TestHasAnyDrivesThePublic404(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[1]`}, "")
	if !r.HasAny([]string{"userDecks", "userCards"}) {
		t.Fatal("HasAny missed a present key")
	}
	if r.HasAny([]string{"userDecks", "userHonors"}) {
		t.Fatal("HasAny reported presence for two absent keys")
	}
	// upload_time counts as present when the row carries one.
	if r.HasAny([]string{"upload_time"}) {
		t.Fatal("upload_time reported present on a row without one")
	}
	r.UploadTime, r.HasUpload = 1, true
	if !r.HasAny([]string{"upload_time"}) {
		t.Fatal("upload_time not reported present")
	}
}

// --- extra -------------------------------------------------------------------

// An unknown key lives in `extra` and must still be servable.
func TestUnknownKeyIsServedFromExtra(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), nil, `{"userBrandNewKey":[{"a":1}]}`)
	v, ok, err := r.RawValue("userBrandNewKey")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if string(v) != `[{"a":1}]` {
		t.Fatalf("got %s", v)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// `?key=updatedResources` is the main mysekai query — that key is 97.9% of a
// mysekai document — and the parent owns no column of its own. A RawValue that
// falls through to `extra` for it answers "absent", which the public face turns
// into a 404. Found by the cutover dress rehearsal: 118 of 675 sampled
// responses 404'd on PostgreSQL while MongoDB served them.
func TestRawValueRebuildsTheFlattenParent(t *testing.T) {
	c := catalog.Mysekai()
	r := newTestRow(t, c, map[string]string{
		"updatedResources.userMysekaiPhotos":      `[{"id":1}]`,
		"updatedResources.userMysekaiHarvestMaps": `[{"m":2}]`,
	}, "")

	raw, ok, err := r.RawValue(c.FlattenKey)
	if err != nil || !ok {
		t.Fatalf("flatten parent not served: ok=%v err=%v", ok, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	for _, k := range []string{"userMysekaiPhotos", "userMysekaiHarvestMaps"} {
		if _, present := got[k]; !present {
			t.Fatalf("child %q missing: %s", k, raw)
		}
	}

	// And it must drive the 404 test, not just the body.
	if !r.HasAny([]string{c.FlattenKey}) {
		t.Fatal("HasAny reported the flatten parent absent; the face would 404")
	}

	// A row with no children at all still has no parent to serve.
	empty := newTestRow(t, c, nil, "")
	if _, ok, _ := empty.RawValue(c.FlattenKey); ok {
		t.Fatal("an empty row served a flatten parent")
	}
	if empty.HasAny([]string{c.FlattenKey}) {
		t.Fatal("HasAny reported a parent on an empty row")
	}
}

// Unknown children parked in `extra` must rejoin their siblings, not vanish.
func TestFlattenParentIncludesUnknownChildrenFromExtra(t *testing.T) {
	c := catalog.Mysekai()
	r := newTestRow(t, c,
		map[string]string{"updatedResources.userMysekaiPhotos": `[1]`},
		`{"updatedResources":{"userBrandNewChild":[2]}}`)
	raw, ok, err := r.RawValue(c.FlattenKey)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"userMysekaiPhotos", "userBrandNewChild"} {
		if _, present := got[k]; !present {
			t.Fatalf("%q missing from the rebuilt parent: %s", k, raw)
		}
	}
}

// --- derived fields ----------------------------------------------------------

// NormalizeProviderResponse synthesises `_idString` beside any top-level `_id`
// and `userIdString` inside a NESTED userGamedata. They are stored nowhere, so a
// byte-splicing path has to add them or every client loses a field — and it is
// the field that carries the id safely, since the numeric form exceeds what a
// JavaScript number represents. Found by the cutover dress rehearsal.
func TestIDStringIsSynthesisedBesideTopLevelID(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[1]`}, "")
	body, err := r.PrivateBody(nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["_idString"] != "28808221489823746" {
		t.Fatalf("_idString = %#v: %s", got["_idString"], body)
	}
}

func TestIDStringAppearsInAKeyedProjectionToo(t *testing.T) {
	r := newTestRow(t, catalog.Mysekai(), nil, "")
	body, err := r.MysekaiBody([]string{"_id"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["_idString"]; !ok {
		t.Fatalf("_idString missing from a ?key=_id response: %s", body)
	}
}

// userIdString is gated on NESTING: a bare ?key=userGamedata response does not
// carry it, because NormalizeProviderResponse passes objectName="" for the root.
func TestUserIDStringOnlyOnNestedUserGamedata(t *testing.T) {
	stored := `{"userId":28808221489823746,"name":"n","rank":5}`
	r := newTestRow(t, catalog.Suite(), map[string]string{"userGamedata": stored}, "")

	nested, err := r.SuiteBody([]string{"userGamedata"}, false)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]map[string]any
	if err := json.Unmarshal(nested, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["userGamedata"]["userIdString"] != "28808221489823746" {
		t.Fatalf("nested userGamedata lacks userIdString: %s", nested)
	}

	bare, err := r.SuiteBody([]string{"userGamedata"}, true)
	if err != nil {
		t.Fatal(err)
	}
	var flat map[string]any
	if err := json.Unmarshal(bare, &flat); err != nil {
		t.Fatal(err)
	}
	if _, leaked := flat["userIdString"]; leaked {
		t.Fatalf("bare userGamedata gained userIdString: %s", bare)
	}
}

// A value the id cannot be derived from must OMIT the derived field rather than
// invent one.
func TestIDStringDerivation(t *testing.T) {
	cases := map[string]string{
		`28808221489823746`:  "28808221489823746",
		`"already-a-string"`: "already-a-string",
		`1.0`:                "1",
	}
	for in, want := range cases {
		got, ok := idStringFromRaw([]byte(in))
		if !ok || got != want {
			t.Errorf("idStringFromRaw(%s) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{`1.5`, `""`, `null`, `[1]`, `{}`, ``} {
		if _, ok := idStringFromRaw([]byte(in)); ok {
			t.Errorf("idStringFromRaw(%s) invented a value", in)
		}
	}
}
