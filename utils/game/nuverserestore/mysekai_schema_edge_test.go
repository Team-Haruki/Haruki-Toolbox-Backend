package nuverserestore

import (
	json "encoding/json/v2"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
)

// typedSchema exercises every scalar kind, a trailing-null union and a short-name
// record reference resolved inside the declaring namespace.
const typedSchema = `[
 {"type":"record","name":"Child","namespace":"Test","fields":[{"name":"v","type":"long","msgpack_key":0}]},
 {"type":"record","name":"UserMysekaiHarvestMap","namespace":"Test","fields":[
  {"name":"id","type":"int","msgpack_key":0},
  {"name":"flag","type":"boolean","msgpack_key":1},
  {"name":"ratio","type":"double","msgpack_key":2},
  {"name":"weight","type":["float","null"],"msgpack_key":3},
  {"name":"children","type":{"type":"array","items":"Child"},"msgpack_key":4}
 ]}
]`

func typedRestorer(t *testing.T) *MysekaiRestorer {
	t.Helper()
	defs, err := parseSchema([]byte(typedSchema))
	if err != nil {
		t.Fatalf("parseSchema: %v", err)
	}
	return &MysekaiRestorer{regions: map[string]layout{"cn": {fields: defs, hash: "typed"}}}
}

func TestParseSchemaResolvesNamespaceRelativeReferencesAndUnions(t *testing.T) {
	defs, err := parseSchema([]byte(typedSchema))
	if err != nil {
		t.Fatal(err)
	}
	want := []definition{
		{name: "id", kind: "int"},
		{name: "flag", kind: "boolean"},
		{name: "ratio", kind: "double"},
		{name: "weight", kind: "float", nullable: true},
		{name: "children", kind: "array", children: []definition{{name: "v", kind: "long"}}},
	}
	if !reflect.DeepEqual(defs, want) {
		t.Fatalf("definitions = %#v", defs)
	}
}

func TestParseSchemaRejectsStructuralErrors(t *testing.T) {
	harvest := func(fields string) string {
		return `{"type":"record","name":"UserMysekaiHarvestMap","fields":[` + fields + `]}`
	}
	suiteUser := func(ns, fieldType string) string {
		return `{"type":"record","name":"SuiteUser","namespace":"` + ns + `","fields":[{"name":"userMysekaiHarvestMaps","msgpack_key":0,"type":` + fieldType + `}]}`
	}
	tests := map[string]struct {
		schema string
		want   string
	}{
		"too deep":                 {strings.Repeat("[", 66) + strings.Repeat("]", 66), "nesting exceeds 64"},
		"unnamed record":           {`{"type":"record","fields":[]}`, "unnamed record"},
		"duplicate record":         {`[` + harvest(`{"name":"a","type":"int","msgpack_key":0}`) + `,` + harvest(`{"name":"a","type":"int","msgpack_key":0}`) + `]`, "duplicate record UserMysekaiHarvestMap"},
		"nested unnamed record":    {harvest(`{"name":"a","msgpack_key":0,"type":{"type":"array","items":{"type":"record","fields":[]}}}`), "unnamed record"},
		"ambiguous suite field":    {`[` + suiteUser("A", `{"type":"array","items":"int"}`) + `,` + suiteUser("B", `{"type":"array","items":"int"}`) + `]`, "ambiguous SuiteUser harvest field"},
		"suite field bad union":    {suiteUser("A", `["null","int","string"]`), "unsupported union"},
		"suite field not array":    {suiteUser("A", `"int"`), "harvest field must be an array"},
		"ambiguous harvest record": {`[{"type":"record","name":"A.UserMysekaiHarvestMap","fields":[]},{"type":"record","name":"B.UserMysekaiHarvestMap","fields":[]}]`, "ambiguous harvest record"},
		"items bad union":          {suiteUser("A", `{"type":"array","items":["a","b","c"]}`), "unsupported union"},
		"items not a record":       {suiteUser("A", `{"type":"array","items":"int"}`), "unresolved harvest record"},
		"empty record":             {harvest(``), "empty harvest record"},
		"field not an object":      {harvest(`"id"`), "invalid field"},
		"non-null union":           {harvest(`{"name":"a","type":["int","string"],"msgpack_key":0}`), "expected nullable union"},
		"field bad union":          {harvest(`{"name":"a","type":["null","int","long"],"msgpack_key":0}`), "unsupported union"},
		"map field":                {harvest(`{"name":"a","type":{"type":"map","values":"int"},"msgpack_key":0}`), "a: unsupported field type"},
		"bytes field":              {harvest(`{"name":"a","type":"bytes","msgpack_key":0}`), "a: unsupported field type bytes"},
		"string msgpack key":       {harvest(`{"name":"a","type":"int","msgpack_key":"0"}`), "integer msgpack_key required"},
		"reserved name":            {harvest(`{"name":"_msgpackExtra","type":"int","msgpack_key":0}`), "invalid or duplicate harvest field"},
		"recursive record": {`{"type":"record","name":"UserMysekaiHarvestMap","namespace":"N","fields":[` +
			`{"name":"self","msgpack_key":0,"type":{"type":"array","items":"UserMysekaiHarvestMap"}}]}`, "recursive or excessively nested"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseSchema([]byte(tc.schema))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseSchema error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRestoreTypedScalarKinds(t *testing.T) {
	r := typedRestorer(t)
	raw, err := r.JSON("cn", []byte(`[[1,true,0.5,null,[[9007199254740993]]],[2,false,3,1.25e1,null]]`))
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	if err := json.Unmarshal(raw, &got, jsonvalue.Numbers); err != nil {
		t.Fatal(err)
	}
	first := got[0].(map[string]any)
	if first["flag"] != true || first["ratio"] != jsonvalue.Number("0.5") || first["weight"] != nil {
		t.Fatalf("first record = %#v", first)
	}
	child := first["children"].([]any)[0].(map[string]any)
	if child["v"] != jsonvalue.Number("9007199254740993") {
		t.Fatalf("child lost precision: %#v", child)
	}
	second := got[1].(map[string]any)
	if second["ratio"] != jsonvalue.Number("3") || second["weight"] != jsonvalue.Number("1.25e1") || second["children"] != nil {
		t.Fatalf("second record = %#v", second)
	}

	for input, want := range map[string]string{
		`[[1,"yes",0.5,null,[]]]`:     "flag: expected boolean",
		`[[1,true,"0.5",null,[]]]`:    "ratio: expected double",
		`[[1,true,0.5,true,[]]]`:      "weight: expected float",
		`[[1.5,true,0.5,null,[]]]`:    "id: expected int",
		`[[1,true,0.5,null,[[1.5]]]]`: "children[0].v: expected long",
	} {
		if _, err := r.JSON("cn", []byte(input)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", input, err, want)
		}
	}
}

func TestRestoreAcceptsNativeGoNumbers(t *testing.T) {
	r := typedRestorer(t)
	record := func(id, ratio any) map[string]any {
		return map[string]any{HarvestMaps: []any{[]any{id, true, ratio, nil, []any{}}}}
	}
	valid := []struct{ id, ratio any }{
		{int64(5), float64(0.25)},
		{uint8(5), float32(1.5)},
		{float64(5), int(2)},
		{float32(7), uint64(3)},
	}
	for _, tc := range valid {
		got, err := r.Document("cn", record(tc.id, tc.ratio))
		if err != nil {
			t.Fatalf("id %T ratio %T: %v", tc.id, tc.ratio, err)
		}
		restored := got[HarvestMaps].([]any)[0].(map[string]any)
		if restored["id"] != tc.id || restored["ratio"] != tc.ratio {
			t.Fatalf("values changed: %#v", restored)
		}
	}
	invalid := []struct {
		id, ratio any
		want      string
	}{
		{float64(5.5), 1.0, "id: expected int"},
		{math.Inf(1), 1.0, "id: expected int"},
		{math.NaN(), 1.0, "id: expected int"},
		{nil, 1.0, "id: expected int"},
		{"5", 1.0, "id: expected int"},
		{1, math.NaN(), "ratio: expected double"},
		{1, float32(math.Inf(-1)), "ratio: expected double"},
		{1, nil, "ratio: expected double"},
		{1, "1.0", "ratio: expected double"},
		{1, jsonvalue.Number("1e400"), "ratio: expected double"},
	}
	for _, tc := range invalid {
		if _, err := r.Document("cn", record(tc.id, tc.ratio)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("id %#v ratio %#v: error = %v, want %q", tc.id, tc.ratio, err, tc.want)
		}
	}
}

func TestRestoreReportsErrorsOnEveryPath(t *testing.T) {
	r := testRestorer(t)
	if _, err := r.JSON("cn", []byte(`[`)); err == nil {
		t.Error("malformed JSON accepted")
	}
	if _, err := r.JSON("cn", []byte(`{"a":1}`)); err == nil || !strings.Contains(err.Error(), "expected array") {
		t.Errorf("object column error = %v", err)
	}
	if _, err := r.JSON("cn", []byte(`[{"mysekaiSiteId":1,"userMysekaiSiteHarvestFixtures":5}]`)); err == nil ||
		!strings.Contains(err.Error(), "userMysekaiSiteHarvestFixtures: expected array") {
		t.Errorf("nested map child error = %v", err)
	}
	nested := map[string]any{"updatedResources": map[string]any{HarvestMaps: []any{true}}}
	if _, err := r.Document("cn", nested); err == nil || !strings.Contains(err.Error(), "updatedResources."+HarvestMaps+"[0]") {
		t.Errorf("updatedResources error = %v", err)
	}
	untouched := map[string]any{"updatedResources": map[string]any{"other": 1}}
	got, err := r.Document("cn", untouched)
	if err != nil || !reflect.DeepEqual(got, untouched) {
		t.Errorf("document without harvest maps changed: %#v %v", got, err)
	}
}
