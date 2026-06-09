package nuversestruct

import (
	"os"
	"path/filepath"
	"testing"

	"haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	"haruki-suite/utils/orderedmsgpack"
	"haruki-suite/utils/sekai"
)

func TestCompareSuiteRestoreReportsShapeChanges(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	samplePath := filepath.Join(dir, "sample.msgpack")

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
		SampleMsgpackPath: samplePath,
		SchemaPath:        schemaPath,
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
	if len(report.AddedFields) != 0 || len(report.RemovedFields) != 0 || len(report.TypeChanged) != 0 {
		t.Fatalf("single-schema compare should not report diffs: %#v", report)
	}
	if _, err := report.MarshalJSONDeterministic(); err != nil {
		t.Fatalf("marshal report: %v", err)
	}
}

func TestCompareSuiteRestoreWithBaselineSchemaReportsChanges(t *testing.T) {
	dir := t.TempDir()
	baselinePath := filepath.Join(dir, "baseline.avsc")
	schemaPath := filepath.Join(dir, "schema.avsc")
	samplePath := filepath.Join(dir, "sample.msgpack")

	if err := os.WriteFile(baselinePath, []byte(`{
	  "type": "record",
	  "name": "SuiteUser",
	  "fields": [
	    {
	      "name": "userCards",
	      "type": {
	        "type": "array",
	        "items": {
	          "type": "record",
	          "name": "UserCard",
	          "fields": [
	            {"name": "cardId", "type": "long", "msgpack_key": 0},
	            {"name": "level", "type": "int", "msgpack_key": 1}
	          ]
	        }
	      },
	      "msgpack_key": "userCards"
	    }
	  ]
	}`), 0o600); err != nil {
		t.Fatalf("write baseline schema: %v", err)
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
		SampleMsgpackPath:  samplePath,
		BaselineSchemaPath: baselinePath,
		SchemaPath:         schemaPath,
	})
	if err != nil {
		t.Fatalf("CompareSuiteRestore returned error: %v", err)
	}
	if !containsString(report.AddedFields, "userCards[].episodes") {
		t.Fatalf("expected generated nested field to be reported as added, got %#v", report.AddedFields)
	}
}

func TestCompareSuiteRestoreRawUploadInput(t *testing.T) {
	originalCfg := config.Cfg
	t.Cleanup(func() {
		config.Cfg = originalCfg
	})
	config.Cfg.SekaiClient.OtherServerAESKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	config.Cfg.SekaiClient.OtherServerAESIV = "0102030405060708090a0b0c0d0e0f10"

	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	samplePath := filepath.Join(dir, "sample.raw")

	if err := os.WriteFile(schemaPath, readTestdata(t, "suite_schema.avro.json"), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	msgpackBytes, err := orderedmsgpack.Marshal(map[string]any{
		"userCards": []any{[]any{int64(100), int64(30)}},
	})
	if err != nil {
		t.Fatalf("marshal sample: %v", err)
	}
	cryptor, err := sekai.NewSekaiCryptorFromHex(
		config.Cfg.SekaiClient.OtherServerAESKey,
		config.Cfg.SekaiClient.OtherServerAESIV,
	)
	if err != nil {
		t.Fatalf("create cryptor: %v", err)
	}
	rawUpload, err := cryptor.Pack(msgpackBytes)
	if err != nil {
		t.Fatalf("pack raw upload: %v", err)
	}
	if err := os.WriteFile(samplePath, rawUpload, 0o600); err != nil {
		t.Fatalf("write raw upload sample: %v", err)
	}

	report, err := CompareSuiteRestore(CompareOptions{
		SampleMsgpackPath: samplePath,
		SchemaPath:        schemaPath,
		InputFormat:       InputFormatRawUpload,
		Server:            harukiUtils.SupportedDataUploadServerJP,
	})
	if err != nil {
		t.Fatalf("CompareSuiteRestore raw upload returned error: %v", err)
	}
	if report.GeneratedStructureCount != 2 {
		t.Fatalf("generated structure count = %d, want 2", report.GeneratedStructureCount)
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
