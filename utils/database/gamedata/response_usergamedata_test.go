package gamedata

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// storedUserGamedata carries the seven served fields plus two that must never
// leave the store.
const storedUserGamedata = `{"userId":28808221489823746,"name":"n","deck":1,"exp":2,"totalExp":3,` +
	`"coin":4,"rank":5,"secretToken":"nope","registeredAt":123}`

var bannedUserGamedataFields = []string{"secretToken", "registeredAt"}

// assertFilteredUserGamedata checks one served userGamedata object: exactly the
// allowlisted fields (plus userIdString when nested), nothing else, and the id
// as an exact literal.
func assertFilteredUserGamedata(t *testing.T, raw []byte, nested bool) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	want := len(userGamedataAllowedFields)
	if nested {
		want++
		if got[userIDStringKey] != "28808221489823746" {
			t.Fatalf("nested userGamedata lacks userIdString: %s", raw)
		}
	} else if _, ok := got[userIDStringKey]; ok {
		t.Fatalf("bare userGamedata gained userIdString: %s", raw)
	}
	if len(got) != want {
		t.Fatalf("served %d fields, want %d: %s", len(got), want, raw)
	}
	for _, banned := range bannedUserGamedataFields {
		if _, leaked := got[banned]; leaked {
			t.Fatalf("%s leaked: %s", banned, raw)
		}
	}
	if !containsSub(string(raw), "28808221489823746") {
		t.Fatalf("userId lost precision: %s", raw)
	}
}

// ?key=userGamedata on the private surface (single-key branch) must serve the
// same seven fields as the whole document, not the stored object.
func TestPrivateSingleKeyUserGamedataIsFiltered(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userGamedata": storedUserGamedata}, "")
	body, err := r.PrivateBody([]string{"userGamedata"})
	if err != nil {
		t.Fatal(err)
	}
	assertFilteredUserGamedata(t, body, false)
}

// The multi-key branch nests userGamedata in an object, so it gets the same
// shape as the whole document's member, userIdString included.
func TestPrivateMultiKeyUserGamedataIsFiltered(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{
		"userGamedata": storedUserGamedata,
		"userCards":    `[{"cardId":1}]`,
	}, "")
	body, err := r.PrivateBody([]string{"userCards", "userGamedata"})
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]jsontext.Value
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	if string(obj["userCards"]) != `[{"cardId":1}]` {
		t.Fatalf("other keys must pass through untouched: %s", body)
	}
	assertFilteredUserGamedata(t, obj["userGamedata"], true)
}

// The keyed and whole-document renders of userGamedata must agree byte for
// byte, so a client switching to ?key= sees no change in what it gets.
func TestPrivateKeyedUserGamedataMatchesWholeDocument(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userGamedata": storedUserGamedata}, "")
	whole, err := r.PrivateBody(nil)
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := r.PrivateBody([]string{"userGamedata", "userCards"})
	if err != nil {
		t.Fatal(err)
	}
	var w, k map[string]jsontext.Value
	if err := json.Unmarshal(whole, &w); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(keyed, &k); err != nil {
		t.Fatal(err)
	}
	if string(w["userGamedata"]) != string(k["userGamedata"]) {
		t.Fatalf("whole %s\nkeyed %s", w["userGamedata"], k["userGamedata"])
	}
	assertFilteredUserGamedata(t, w["userGamedata"], true)
}

// Absent userGamedata keeps the private surface's null in both branches: the
// filter must not turn a missing value into {}.
func TestPrivateAbsentUserGamedataStaysNull(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userCards": `[]`}, "")
	single, err := r.PrivateBody([]string{"userGamedata"})
	if err != nil {
		t.Fatal(err)
	}
	if string(single) != "null" {
		t.Fatalf("single = %s, want null", single)
	}
	multi, err := r.PrivateBody([]string{"userCards", "userGamedata"})
	if err != nil {
		t.Fatal(err)
	}
	if string(multi) != `{"userCards":[],"userGamedata":null}` {
		t.Fatalf("multi = %s", multi)
	}
}

// A stored value that is not an object cannot be filtered; it must fail the
// render rather than pass through whole.
func TestPrivateUndecodableUserGamedataFails(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userGamedata": `[1,2]`}, "")
	if body, err := r.PrivateBody([]string{"userGamedata"}); err == nil {
		t.Fatalf("single key served %s", body)
	}
	if body, err := r.PrivateBody([]string{"userGamedata", "userCards"}); err == nil {
		t.Fatalf("multi key served %s", body)
	}
}

// The mysekai catalog has no userGamedata column, so such a member would live
// in `extra`. Every mysekai render (keyed, whole public, whole private) must
// filter it too.
func TestMysekaiExtraUserGamedataIsFiltered(t *testing.T) {
	c := catalog.Mysekai()
	if _, place := c.Resolve(userGamedataKey); place != catalog.PlaceUnknown {
		t.Fatalf("test assumes userGamedata is not a mysekai column (placement %v)", place)
	}
	extra := `{"userGamedata":` + storedUserGamedata + `}`
	r := newTestRow(t, c, nil, extra)

	keyed, err := r.MysekaiBody([]string{"userGamedata"})
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]jsontext.Value
	if err := json.Unmarshal(keyed, &obj); err != nil {
		t.Fatalf("%v\n%s", err, keyed)
	}
	assertFilteredUserGamedata(t, obj["userGamedata"], true)

	for _, whole := range []func() ([]byte, error){
		func() ([]byte, error) { return r.MysekaiBody(nil) },
		func() ([]byte, error) { return r.PrivateBody(nil) },
	} {
		body, err := whole()
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]jsontext.Value
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("%v\n%s", err, body)
		}
		assertFilteredUserGamedata(t, doc["userGamedata"], true)
	}

	single, err := r.PrivateBody([]string{"userGamedata"})
	if err != nil {
		t.Fatal(err)
	}
	assertFilteredUserGamedata(t, single, false)
}

// A malformed extra userGamedata fails the whole-document render instead of
// being spliced in whole.
func TestMysekaiUndecodableExtraUserGamedataFails(t *testing.T) {
	r := newTestRow(t, catalog.Mysekai(), nil, `{"userGamedata":"opaque"}`)
	if body, err := r.MysekaiBody(nil); err == nil {
		t.Fatalf("served %s", body)
	}
}

// A dotted path into userGamedata is never descended on the private surface,
// so it cannot be used to reach an unfiltered field either.
func TestPrivateDottedUserGamedataPathStaysNull(t *testing.T) {
	r := newTestRow(t, catalog.Suite(), map[string]string{"userGamedata": storedUserGamedata}, "")
	body, err := r.PrivateBody([]string{"userGamedata.secretToken"})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "null" {
		t.Fatalf("dotted path served %s", body)
	}
}

// Other keys are untouched by servedValue, including the similarly named
// userMysekaiGamedata.
func TestServedValueLeavesOtherKeysAlone(t *testing.T) {
	stored := `{"secretish":1}`
	r := newTestRow(t, catalog.Suite(), map[string]string{"userMysekaiGamedata": stored}, "")
	v, ok, err := r.servedValue("userMysekaiGamedata", true)
	if err != nil || !ok || string(v) != stored {
		t.Fatalf("servedValue = %s, %v, %v", v, ok, err)
	}
}
