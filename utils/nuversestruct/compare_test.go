package nuversestruct

import (
	"os"
	"path/filepath"
	"testing"

	"haruki-suite/utils/orderedmsgpack"
)

func TestCompareSuiteRestoreReportsShapeChanges(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "current.json")
	schemaPath := filepath.Join(dir, "schema.json")
	samplePath := filepath.Join(dir, "sample.msgpack")

	if err := os.WriteFile(currentPath, []byte(`{"userCards":["cardId","level"]}`), 0o600); err != nil {
		t.Fatalf("write current structures: %v", err)
	}
	if err := os.WriteFile(schemaPath, readTestdata(t, "suite_schema.avro.json"), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	msgpackBytes, err := orderedmsgpack.Marshal(map[string]any{
		"userCards": []any{
			[]any{int64(100), int64(30), []any{[]any{int64(1), "read"}}},
		},
	})
	if err != nil {
		t.Fatalf("marshal sample: %v", err)
	}
	if err := os.WriteFile(samplePath, msgpackBytes, 0o600); err != nil {
		t.Fatalf("write sample msgpack: %v", err)
	}

	report, err := CompareSuiteRestore(CompareOptions{
		SampleMsgpackPath:     samplePath,
		CurrentStructuresPath: currentPath,
		SchemaPath:            schemaPath,
	})
	if err != nil {
		t.Fatalf("CompareSuiteRestore returned error: %v", err)
	}
	if report.GeneratedStructureCount != 2 {
		t.Fatalf("generated structure count = %d, want 2", report.GeneratedStructureCount)
	}
	if report.ComparedTopLevelFields == 0 {
		t.Fatalf("expected compared fields")
	}
	if !containsString(report.AddedFields, "userCards[].episodes") {
		t.Fatalf("expected generated nested field to be reported as added, got %#v", report.AddedFields)
	}
	if _, err := report.MarshalJSONDeterministic(); err != nil {
		t.Fatalf("marshal report: %v", err)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
