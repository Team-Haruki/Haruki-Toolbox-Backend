package nuversestruct

import "testing"

func TestIsStructToolSchema(t *testing.T) {
	schema := readTestdata(t, "suite_schema.avro.json")
	if !IsStructToolSchema(schema) {
		t.Fatalf("IsStructToolSchema should detect StructTool/custom Avro schema")
	}

	legacy := []byte(`{"userCards":["cardId","level"]}`)
	if IsStructToolSchema(legacy) {
		t.Fatalf("IsStructToolSchema should not detect legacy suite structure JSON")
	}
}

func TestNewRestorerFromBytes(t *testing.T) {
	restorer, err := NewRestorerFromBytes(readTestdata(t, "suite_schema.avro.json"))
	if err != nil {
		t.Fatalf("NewRestorerFromBytes returned error: %v", err)
	}

	data := map[string]any{
		"userHonors": []any{[]any{int64(1), int64(5)}},
	}
	restored := restorer.RestoreFields(data)
	honor, ok := restored["userHonors"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatalf("userHonors should be restored to map, got %#v", restored["userHonors"])
	}
	if honor["honorId"] != int64(1) || honor["level"] != int64(5) {
		t.Fatalf("unexpected restored honor: %#v", honor)
	}
}
