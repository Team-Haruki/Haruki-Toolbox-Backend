package mysekairestore

import (
	"bytes"
	json "encoding/json/v2"
	"reflect"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/jsonvalue"
)

const compact = `[[5,[[111,-3,7,20,"spawned",null]],[["mysekai_material",24,-3,7,10,13,"before_drop",2,null]]]]`

func TestRestoreHarvestContract(t *testing.T) {
	r := testRestorer(t)
	raw, err := r.JSON("cn", []byte(compact))
	if err != nil {
		t.Fatal(err)
	}
	expected := `[{"mysekaiSiteId":5,"userMysekaiSiteHarvestFixtures":[{"mysekaiSiteHarvestFixtureId":111,"positionX":-3,"positionZ":7,"hp":20,"userMysekaiSiteHarvestFixtureStatus":"spawned","mysekaiSiteHarvestSpawnLimitedRelationGroupId":null}],"userMysekaiSiteHarvestResourceDrops":[{"resourceType":"mysekai_material","resourceId":24,"positionX":-3,"positionZ":7,"hp":10,"seq":13,"mysekaiSiteHarvestResourceDropStatus":"before_drop","quantity":2,"mysekaiSiteHarvestSpawnLimitedRelationGroupId":null}]}]`
	var got, want any
	if err = json.Unmarshal(raw, &got, jsonvalue.Numbers); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(expected), &want, jsonvalue.Numbers); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s", raw)
	}
	again, err := r.JSON("cn", raw)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("not idempotent: %s %v", again, err)
	}
}

func TestMixedNestedRecordsPreserveUnknownAndNumbers(t *testing.T) {
	r := testRestorer(t)
	raw := []byte(`[{"mysekaiSiteId":5,"unknown":7486493092749597481,"userMysekaiSiteHarvestFixtures":[[111,-3,7,20,"spawned",912,"future"],{"custom":true}],"userMysekaiSiteHarvestResourceDrops":[]},[7,[],[]]]`)
	result, err := r.JSON("cn", raw)
	if err != nil {
		t.Fatal(err)
	}
	var values []any
	if err = json.Unmarshal(result, &values, jsonvalue.Numbers); err != nil {
		t.Fatal(err)
	}
	first := values[0].(map[string]any)
	if first["unknown"] != jsonvalue.Number("7486493092749597481") {
		t.Fatal("lost numeric precision")
	}
	fixtures := first["userMysekaiSiteHarvestFixtures"].([]any)
	if fixtures[0].(map[string]any)[Extra].([]any)[0] != "future" || fixtures[1].(map[string]any)["custom"] != true {
		t.Fatalf("lost fields: %s", result)
	}
}

func TestInvalidRecordsFailWithoutMutation(t *testing.T) {
	for _, input := range []string{`[[5]]`, `[["5",[],[]]]`, `[[5,{},[]]]`, `[[5,[null],[]]]`, `[[5,[],[["x",1,2,3,4,5,6,7,null]]]]`, `[true]`} {
		t.Run(input, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(input), &value, jsonvalue.Numbers); err != nil {
				t.Fatal(err)
			}
			data := map[string]any{HarvestMaps: value}
			before, _ := json.Marshal(data, json.Deterministic(true))
			r := testRestorer(t)
			if _, err := r.Document("cn", data); err == nil {
				t.Fatal("expected error")
			}
			after, _ := json.Marshal(data, json.Deterministic(true))
			if !bytes.Equal(before, after) {
				t.Fatal("mutated input")
			}
		})
	}
}

func TestRegionProfilesAndBothDocumentShapes(t *testing.T) {
	profiles := map[string]string{"cn": "testdata/cn-6.4.0.harvest_map.avsc", "tw": "testdata/cn-6.4.0.harvest_map.avsc", "kr": ""}
	r, err := New(profiles)
	if err != nil {
		t.Fatal(err)
	}
	profiles["tw"] = ""
	for _, region := range []string{"cn", "tw", "kr", "jp"} {
		for _, nested := range []bool{false, true} {
			var value any
			if err := json.Unmarshal([]byte(compact), &value, jsonvalue.Numbers); err != nil {
				t.Fatal(err)
			}
			data := map[string]any{HarvestMaps: value}
			if nested {
				data = map[string]any{"updatedResources": data}
			}
			got, err := r.Document(region, data)
			if err != nil {
				t.Fatal(err)
			}
			if nested {
				got = got["updatedResources"].(map[string]any)
			}
			_, restored := got[HarvestMaps].([]any)[0].(map[string]any)
			if restored != (region == "cn" || region == "tw") {
				t.Fatalf("wrong region policy %s", region)
			}
			if _, ok := value.([]any)[0].([]any); !ok {
				t.Fatal("mutated input")
			}
		}
	}
	disabled, _ := New(map[string]string{})
	if disabled.Fingerprint("cn") != "" {
		t.Fatal("explicit disable ignored")
	}
	defaults, _ := New(nil)
	if defaults.Fingerprint("cn") != "" || defaults.Fingerprint("tw") != "" || defaults.Fingerprint("kr") != "" {
		t.Fatal("wrong defaults")
	}
	for _, p := range []map[string]string{{"cn": "typo"}, {"other": "testdata/cn-6.4.0.harvest_map.avsc"}} {
		if _, err := New(p); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}

func TestEmptyAndNullableTail(t *testing.T) {
	r := testRestorer(t)
	for _, input := range []string{`null`, `[]`, `[[5,[],[]]]`, `[[5,[[111,1,2,3,"spawned"]],[]]]`, `[[5,null,null]]`} {
		if _, err := r.JSON("cn", []byte(input)); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
	}
	for _, typ := range []string{"mysekai_material", "mysekai_item", "mysekai_fixture", "mysekai_blueprint", "mysekai_music_record"} {
		raw := bytes.ReplaceAll([]byte(compact), []byte("mysekai_material"), []byte(typ))
		result, err := r.JSON("cn", raw)
		if err != nil || !bytes.Contains(result, []byte(typ)) {
			t.Fatalf("%s: %v", typ, err)
		}
	}
}

func testRestorer(t *testing.T) *Restorer {
	t.Helper()
	r, err := New(map[string]string{"cn": "testdata/cn-6.4.0.harvest_map.avsc"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
