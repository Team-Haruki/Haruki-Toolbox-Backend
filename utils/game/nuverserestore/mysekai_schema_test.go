package nuverserestore

import (
	"bytes"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewSuiteMatchesHarvestSchema(t *testing.T) {
	suite, err := NewMysekai(map[string]string{"cn": "../../../data/suite_user_cn_6.4.0.avsc"})
	if err != nil {
		t.Fatal(err)
	}
	standalone := testRestorer(t)
	if !reflect.DeepEqual(suite.regions["cn"].fields, standalone.regions["cn"].fields) {
		t.Fatal("new Suite and harvest field names, keys or types differ")
	}
	a, err := suite.JSON("cn", []byte(compact))
	if err != nil {
		t.Fatal(err)
	}
	b, err := standalone.JSON("cn", []byte(compact))
	if err != nil {
		t.Fatal(err)
	}
	var av, bv any
	if err = json.Unmarshal(a, &av); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &bv); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(av, bv) {
		t.Fatal("different restored values")
	}
	if _, err = NewMysekai(map[string]string{"cn": "../../../data/suite_user.avsc"}); err == nil {
		t.Fatal("old string-key schema must not interpret positional harvest arrays")
	}
}
func TestSchemaFileReplacementChangesCacheAndKeepsLiveInstance(t *testing.T) {
	raw, err := os.ReadFile("testdata/cn-6.4.0.harvest_map.avsc")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "suite.avsc")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := NewMysekai(map[string]string{"cn": path})
	if err != nil {
		t.Fatal(err)
	}
	// A schema update, at the same path, determines the field name used at runtime.
	updated := bytes.ReplaceAll(raw, []byte(`"quantity"`), []byte(`"dropQuantity"`))
	if err = os.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
	second, err := NewMysekai(map[string]string{"cn": path})
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint("cn") == second.Fingerprint("cn") {
		t.Fatal("same-path replacement reused cache version")
	}
	a, _ := first.JSON("cn", []byte(compact))
	b, _ := second.JSON("cn", []byte(compact))
	if !bytes.Contains(a, []byte(`"quantity"`)) || !bytes.Contains(b, []byte(`"dropQuantity"`)) {
		t.Fatal("schema was hardcoded or live instance mutated")
	}
}
func TestSchemaListAndQualifiedReferences(t *testing.T) {
	raw, err := os.ReadFile("testdata/cn-6.4.0.harvest_map.avsc")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	records := []any{}
	var extract func(any) any
	extract = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				x[k] = extract(c)
			}
			if x["type"] == "record" {
				records = append(records, x)
				return x["name"]
			}
		case []any:
			for i, c := range x {
				x[i] = extract(c)
			}
		}
		return v
	}
	harvestName := extract(root)
	records = append(records, map[string]any{"type": "record", "name": "Sekai.SuiteUser", "fields": []any{map[string]any{"name": HarvestMaps, "msgpack_key": HarvestMaps, "type": map[string]any{"type": "array", "items": harvestName}}}})
	list, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := parseSchema(list)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(defs, testRestorer(t).regions["cn"].fields) {
		t.Fatal("schema list references were not resolved")
	}
}
func TestInvalidSchemasReturnErrors(t *testing.T) {
	raw, err := os.ReadFile("testdata/cn-6.4.0.harvest_map.avsc")
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"missing":      []byte(`{"type":"record","name":"Other","fields":[]}`),
		"malformed":    []byte(`{`),
		"gap":          bytes.Replace(raw, []byte(`"msgpack_key": 0`), []byte(`"msgpack_key": 9`), 1),
		"duplicate":    bytes.Replace(raw, []byte(`"msgpack_key": 1`), []byte(`"msgpack_key": 0`), 1),
		"unsafe":       bytes.Replace(raw, []byte(`"mysekaiSiteId"`), []byte(`"$site"`), 1),
		"unknown-type": bytes.Replace(raw, []byte(`"type": "int"`), []byte(`"type": "MissingRecord"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSchema(input); err == nil {
				t.Fatal("invalid schema accepted")
			}
		})
	}
	if _, err := NewMysekai(map[string]string{"cn": filepath.Join(t.TempDir(), "missing.avsc")}); err == nil {
		t.Fatal("missing file accepted")
	}
	var disabled *MysekaiRestorer
	if got, err := disabled.JSON("cn", []byte(compact)); err != nil || string(got) != compact {
		t.Fatal("nil restorer must disable conversion")
	}
}
