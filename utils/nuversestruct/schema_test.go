package nuversestruct

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestGenerateSuiteStructuresFromAvroSchema(t *testing.T) {
	got, err := GenerateSuiteStructures(readTestdata(t, "suite_schema.avro.json"))
	if err != nil {
		t.Fatalf("GenerateSuiteStructures returned error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("generated structure count = %d, want 2", len(got))
	}

	cards := got["userCards"]
	if len(cards) != 3 {
		t.Fatalf("userCards fields = %#v", cards)
	}
	if cards[0] != "cardId" || cards[1] != "level" {
		t.Fatalf("userCards order = %#v", cards)
	}
	nested, ok := cards[2].([]any)
	if !ok || len(nested) != 2 || nested[0] != "episodes" {
		t.Fatalf("nested episodes structure = %#v", cards[2])
	}
	children, ok := nested[1].([]any)
	if !ok || len(children) != 2 || children[0] != "cardEpisodeId" || children[1] != "scenarioStatus" {
		t.Fatalf("nested episodes children = %#v", nested[1])
	}
}

func TestMarshalSuiteStructuresDeterministic(t *testing.T) {
	schema := readTestdata(t, "suite_schema.avro.json")
	first, err := MarshalGeneratedStructuresFromSchema(schema)
	if err != nil {
		t.Fatalf("first marshal returned error: %v", err)
	}
	second, err := MarshalGeneratedStructuresFromSchema(schema)
	if err != nil {
		t.Fatalf("second marshal returned error: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("marshaled structures should be deterministic")
	}
	if golden := readTestdata(t, "generated_structures.golden.json"); !bytes.Equal(first, golden) {
		t.Fatalf("generated structures differ from golden\n got: %s\nwant: %s", first, golden)
	}

	var decoded map[string]any
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("generated json should be valid: %v", err)
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}
