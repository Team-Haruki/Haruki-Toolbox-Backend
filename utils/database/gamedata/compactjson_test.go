package gamedata

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestIsCompactValue(t *testing.T) {
	cases := map[string]bool{
		`{"a":[1]}`:      true,
		`  {"a":[1]}`:    true,
		"\n\t{\"a\":[]}": true,
		`[{"a":1}]`:      false,
		`  [1,2]`:        false,
		``:               false,
		`null`:           false,
	}
	for in, want := range cases {
		if got := IsCompactValue([]byte(in)); got != want {
			t.Errorf("IsCompactValue(%q) = %v, want %v", in, got, want)
		}
	}
}

// A row-form value must come back untouched, byte for byte: callers hand every
// compact-class column through this function without inspecting it.
func TestRowFormPassesThroughUnchanged(t *testing.T) {
	in := []byte(`[{"musicId":1,"score":100},{"musicId":2,"score":9007199254740993}]`)
	out, err := ExpandCompactJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatalf("row form was rewritten:\n got %s\nwant %s", out, in)
	}
}

func TestExpandRestoresRowsAndDictionary(t *testing.T) {
	compact := []byte(`{"__ENUM__":{"playResult":["full_combo","clear"]},` +
		`"musicId":[1,2],"playResult":[0,1],"score":[100,200]}`)
	out, err := ExpandCompactJSON(compact)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"musicId":1,"playResult":"full_combo","score":100},` +
		`{"musicId":2,"playResult":"clear","score":200}]`
	if string(out) != want {
		t.Fatalf("\n got %s\nwant %s", out, want)
	}
}

// Game user ids exceed 2^53. Any float64 hop corrupts them, so the expander must
// carry number literals through as their exact source bytes.
func TestBigIntegerSurvivesExpansion(t *testing.T) {
	const big = "28808221489823746"
	compact := []byte(`{"userId":[` + big + `,` + big + `],"n":[1,2]}`)
	out, err := ExpandCompactJSON(compact)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"userId":` + big + `,"n":1},{"userId":` + big + `,"n":2}]`
	if string(out) != want {
		t.Fatalf("\n got %s\nwant %s", out, want)
	}
}

// Number literals must not be reformatted: 1.50 stays 1.50, 1e2 stays 1e2.
func TestNumberLiteralsAreNotReformatted(t *testing.T) {
	compact := []byte(`{"a":[1.50,1e2],"b":[1,2]}`)
	out, err := ExpandCompactJSON(compact)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"a":1.50,"b":1},{"a":1e2,"b":2}]`
	if string(out) != want {
		t.Fatalf("\n got %s\nwant %s", out, want)
	}
}

// An index outside the dictionary becomes null (NullInvalidEnumValue), which is
// what the MongoDB path does today.
func TestOutOfRangeEnumIndexBecomesNull(t *testing.T) {
	compact := []byte(`{"__ENUM__":{"k":["only"]},"k":[0,7],"n":[1,2]}`)
	out, err := ExpandCompactJSON(compact)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"k":"only","n":1},{"k":null,"n":2}]`
	if string(out) != want {
		t.Fatalf("\n got %s\nwant %s", out, want)
	}
}

// The dictionary is PER DOCUMENT. Two documents using the same integer index for
// different labels must expand differently — a global dictionary would silently
// change data.
func TestDictionaryIsPerDocument(t *testing.T) {
	a, err := ExpandCompactJSON([]byte(`{"__ENUM__":{"k":["alpha","beta"]},"k":[0]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ExpandCompactJSON([]byte(`{"__ENUM__":{"k":["beta","alpha"]},"k":[0]}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatalf("same index resolved identically under two dictionaries: %s", a)
	}
	if string(a) != `[{"k":"alpha"}]` || string(b) != `[{"k":"beta"}]` {
		t.Fatalf("a=%s b=%s", a, b)
	}
}

// RestoreColumns truncates to the SHORTEST column. That is production behaviour
// and it is why the migration encoder refuses to emit ragged output; assert it
// so a future change to the shared restore code is caught here.
func TestShortColumnTruncatesTheRowSet(t *testing.T) {
	out, err := ExpandCompactJSON([]byte(`{"a":[1,2,3],"b":[9]}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `[{"a":1,"b":9}]` {
		t.Fatalf("got %s", out)
	}
}

func TestEmptyCompactObjectRestoresToEmptyArray(t *testing.T) {
	out, err := ExpandCompactJSON([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `[]` {
		t.Fatalf("got %s", out)
	}
}

func TestExpandRejectsMalformedJSON(t *testing.T) {
	if _, err := ExpandCompactJSON([]byte(`{"a":[1,}`)); err == nil {
		t.Fatal("malformed compact value accepted")
	}
}

func TestExpandRejectsTrailingBytes(t *testing.T) {
	if _, err := ExpandCompactJSON([]byte(`{"a":[1]} {"b":[2]}`)); err == nil {
		t.Fatal("trailing bytes accepted")
	}
}

// Object key order inside a restored row follows column order, and the expanded
// output must be valid JSON that decodes to the same data.
func TestExpandedOutputIsValidJSON(t *testing.T) {
	compact := []byte(`{"__ENUM__":{"s":["x"]},"s":[0,0],"i":[1,2],"f":[true,false],"z":[null,null]}`)
	out, err := ExpandCompactJSON(compact)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("expanded output is not valid JSON: %v\n%s", err, out)
	}
	want := []map[string]any{
		{"s": "x", "i": float64(1), "f": true, "z": nil},
		{"s": "x", "i": float64(2), "f": false, "z": nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("\n got %v\nwant %v", got, want)
	}
}
