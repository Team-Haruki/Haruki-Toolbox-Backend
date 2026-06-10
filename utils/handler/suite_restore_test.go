package handler

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

func resetSuiteRestorerStateForTest() {
	suiteRestorerOnce = sync.Once{}
	suiteRestorerMap = nil
	suiteRestorerSourceMap = nil
	suiteRestorerLoadFailures = nil
}

func TestGetSuiteRestorerLoadStatusTracksFailures(t *testing.T) {
	originalStructuresFile := harukiConfig.Cfg.RestoreSuite.StructuresFile
	t.Cleanup(func() {
		harukiConfig.Cfg.RestoreSuite.StructuresFile = originalStructuresFile
		resetSuiteRestorerStateForTest()
	})

	resetSuiteRestorerStateForTest()

	tmpDir := t.TempDir()
	validPath := filepath.Join(tmpDir, "suite_user.avsc")
	if err := os.WriteFile(validPath, testStructToolSuiteSchema(), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	missingPath := filepath.Join(tmpDir, "missing.json")
	harukiConfig.Cfg.RestoreSuite.StructuresFile = map[string]string{
		"jp": validPath,
		"en": missingPath,
		"tw": "",
	}

	loadedRegions, failures := GetSuiteRestorerLoadStatus()
	if loadedRegions != 1 {
		t.Fatalf("loadedRegions = %d, want %d", loadedRegions, 1)
	}
	if len(failures) != 1 {
		t.Fatalf("len(failures) = %d, want %d", len(failures), 1)
	}
	if _, ok := failures["en"]; !ok {
		t.Fatalf("failures does not include region %q", "en")
	}
	if _, ok := failures["jp"]; ok {
		t.Fatalf("failures should not include region %q", "jp")
	}
}

func TestLoadSuiteRestorerSupportsStructToolSchema(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "suite_user.avsc")
	if err := os.WriteFile(schemaPath, testStructToolSuiteSchema(), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	restorer, err := loadSuiteRestorer(schemaPath)
	if err != nil {
		t.Fatalf("loadSuiteRestorer returned error: %v", err)
	}

	data := map[string]any{
		"userCards": []any{[]any{int64(100), int64(30)}},
	}
	restored := restorer.RestoreFields(data)
	card, ok := restored["userCards"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatalf("userCards should be restored to map, got %#v", restored["userCards"])
	}
	if card["cardId"] != int64(100) || card["level"] != int64(30) {
		t.Fatalf("unexpected restored card: %#v", card)
	}
}

func testStructToolSuiteSchema() []byte {
	return []byte(`{
	  "type": "record",
	  "name": "SuiteUser",
	  "namespace": "Sekai",
	  "fields": [
	    {
	      "name": "userCards",
	      "type": {
	        "type": "array",
	        "items": {
	          "type": "record",
	          "name": "UserCard",
	          "namespace": "Sekai",
	          "fields": [
	            {"name": "cardId", "type": "long", "msgpack_key": 0},
	            {"name": "level", "type": "int", "msgpack_key": 1}
	          ]
	        }
	      },
	      "msgpack_key": "userCards"
	    }
	  ]
	}`)
}

func TestLoadSuiteRestorerRejectsLegacyStructureJSON(t *testing.T) {
	tmpDir := t.TempDir()
	structurePath := filepath.Join(tmpDir, "legacy.json")
	if err := os.WriteFile(structurePath, []byte(`{"userCards":["cardId","level"]}`), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := loadSuiteRestorer(structurePath); err == nil {
		t.Fatalf("loadSuiteRestorer should reject legacy suite structure JSON")
	}
}

func TestGetSuiteRestorerLoadStatusReturnsFailureMapCopy(t *testing.T) {
	originalStructuresFile := harukiConfig.Cfg.RestoreSuite.StructuresFile
	t.Cleanup(func() {
		harukiConfig.Cfg.RestoreSuite.StructuresFile = originalStructuresFile
		resetSuiteRestorerStateForTest()
	})

	resetSuiteRestorerStateForTest()

	tmpDir := t.TempDir()
	harukiConfig.Cfg.RestoreSuite.StructuresFile = map[string]string{
		"en": filepath.Join(tmpDir, "missing.json"),
	}

	_, failures := GetSuiteRestorerLoadStatus()
	if len(failures) != 1 {
		t.Fatalf("len(failures) = %d, want %d", len(failures), 1)
	}
	failures["en"] = "mutated"

	_, failuresAgain := GetSuiteRestorerLoadStatus()
	if failuresAgain["en"] == "mutated" {
		t.Fatalf("GetSuiteRestorerLoadStatus should return a copy of failures map")
	}
}

func TestRestoreSuiteDatabasePurposeCleansAndRespectsEnabledRegions(t *testing.T) {
	originalStructuresFile := harukiConfig.Cfg.RestoreSuite.StructuresFile
	originalEnableRegions := harukiConfig.Cfg.RestoreSuite.EnableRegions
	originalRemoveKeys := harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys
	t.Cleanup(func() {
		harukiConfig.Cfg.RestoreSuite.StructuresFile = originalStructuresFile
		harukiConfig.Cfg.RestoreSuite.EnableRegions = originalEnableRegions
		harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys = originalRemoveKeys
		resetSuiteRestorerStateForTest()
	})

	resetSuiteRestorerStateForTest()

	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "suite_user.avsc")
	if err := os.WriteFile(schemaPath, testStructToolSuiteSchema(), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	harukiConfig.Cfg.RestoreSuite.StructuresFile = map[string]string{"jp": schemaPath}
	harukiConfig.Cfg.RestoreSuite.EnableRegions = []string{"jp"}
	harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys = []string{"removeMe"}

	data := map[string]any{
		"removeMe":  []any{1},
		"userCards": []any{[]any{int64(100), int64(30)}},
	}
	restored, report, err := RestoreSuite(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeDatabase},
	)
	if err != nil {
		t.Fatalf("RestoreSuite returned error: %v", err)
	}
	if !report.Enabled || !report.RestorerLoaded || report.Purpose != SuiteRestorePurposeDatabase {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Source != schemaPath {
		t.Fatalf("Source = %q, want %q", report.Source, schemaPath)
	}
	if report.RestoredFields != 1 || len(report.FailedFields) != 0 {
		t.Fatalf("restore report mismatch: %#v", report)
	}
	if len(restored["removeMe"].([]any)) != 0 {
		t.Fatalf("database purpose should clean configured suite keys, got %#v", restored["removeMe"])
	}
	card := restored["userCards"].([]any)[0].(map[string]any)
	if card["cardId"] != int64(100) || card["level"] != int64(30) {
		t.Fatalf("unexpected restored card: %#v", card)
	}
}

func TestRestoreSuiteDatabasePurposeSkipsDisabledRegion(t *testing.T) {
	originalStructuresFile := harukiConfig.Cfg.RestoreSuite.StructuresFile
	originalEnableRegions := harukiConfig.Cfg.RestoreSuite.EnableRegions
	t.Cleanup(func() {
		harukiConfig.Cfg.RestoreSuite.StructuresFile = originalStructuresFile
		harukiConfig.Cfg.RestoreSuite.EnableRegions = originalEnableRegions
		resetSuiteRestorerStateForTest()
	})

	resetSuiteRestorerStateForTest()

	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "suite_user.avsc")
	if err := os.WriteFile(schemaPath, testStructToolSuiteSchema(), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	harukiConfig.Cfg.RestoreSuite.StructuresFile = map[string]string{"jp": schemaPath}
	harukiConfig.Cfg.RestoreSuite.EnableRegions = []string{"en"}

	data := map[string]any{"userCards": []any{[]any{int64(100), int64(30)}}}
	restored, report, err := RestoreSuite(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeDatabase},
	)
	if err != nil {
		t.Fatalf("RestoreSuite returned error: %v", err)
	}
	if report.Enabled {
		t.Fatalf("database restore should be disabled for jp, report=%#v", report)
	}
	if _, ok := restored["userCards"].([]any)[0].([]any); !ok {
		t.Fatalf("disabled region should keep compact array, got %#v", restored["userCards"])
	}
}

func TestRestoreSuiteSyncPurposeIgnoresEnabledRegionsAndDoesNotClean(t *testing.T) {
	originalStructuresFile := harukiConfig.Cfg.RestoreSuite.StructuresFile
	originalEnableRegions := harukiConfig.Cfg.RestoreSuite.EnableRegions
	originalRemoveKeys := harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys
	t.Cleanup(func() {
		harukiConfig.Cfg.RestoreSuite.StructuresFile = originalStructuresFile
		harukiConfig.Cfg.RestoreSuite.EnableRegions = originalEnableRegions
		harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys = originalRemoveKeys
		resetSuiteRestorerStateForTest()
	})

	resetSuiteRestorerStateForTest()

	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "suite_user.avsc")
	if err := os.WriteFile(schemaPath, testStructToolSuiteSchema(), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	harukiConfig.Cfg.RestoreSuite.StructuresFile = map[string]string{"jp": schemaPath}
	harukiConfig.Cfg.RestoreSuite.EnableRegions = []string{"en"}
	harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys = []string{"removeMe"}

	data := map[string]any{
		"removeMe":  []any{1},
		"userCards": []any{[]any{int64(100), int64(30)}},
	}
	restored, report, err := RestoreSuite(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync},
	)
	if err != nil {
		t.Fatalf("RestoreSuite returned error: %v", err)
	}
	if !report.Enabled || report.Purpose != SuiteRestorePurposeSync || report.RestoredFields != 1 {
		t.Fatalf("unexpected sync report: %#v", report)
	}
	if len(restored["removeMe"].([]any)) != 1 {
		t.Fatalf("sync purpose should not clean suite keys, got %#v", restored["removeMe"])
	}
	card := restored["userCards"].([]any)[0].(map[string]any)
	if card["cardId"] != int64(100) || card["level"] != int64(30) {
		t.Fatalf("unexpected restored card: %#v", card)
	}
}

func TestRestoreSuiteMissingRestorerReportsWithoutError(t *testing.T) {
	originalStructuresFile := harukiConfig.Cfg.RestoreSuite.StructuresFile
	t.Cleanup(func() {
		harukiConfig.Cfg.RestoreSuite.StructuresFile = originalStructuresFile
		resetSuiteRestorerStateForTest()
	})

	resetSuiteRestorerStateForTest()

	harukiConfig.Cfg.RestoreSuite.StructuresFile = map[string]string{}
	data := map[string]any{"userCards": []any{[]any{int64(100), int64(30)}}}
	restored, report, err := RestoreSuite(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync},
	)
	if err != nil {
		t.Fatalf("RestoreSuite returned error: %v", err)
	}
	if report.RestorerLoaded {
		t.Fatalf("RestorerLoaded should be false, report=%#v", report)
	}
	if report.Region != "jp" || report.Purpose != SuiteRestorePurposeSync {
		t.Fatalf("report identity mismatch: %#v", report)
	}
	if restored == nil || len(restored) != len(data) {
		t.Fatalf("missing restorer should leave data unchanged, got %#v", restored)
	}
	if _, ok := restored["userCards"].([]any)[0].([]any); !ok {
		t.Fatalf("missing restorer should keep compact array, got %#v", restored["userCards"])
	}
}
