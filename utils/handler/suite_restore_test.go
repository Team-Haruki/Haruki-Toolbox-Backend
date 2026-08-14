package handler

import (
	"os"
	"path/filepath"
	"testing"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

func TestSuiteRestoreServiceLoadStatusTracksFailures(t *testing.T) {
	tmpDir := t.TempDir()
	validPath := writeTestSuiteSchema(t, tmpDir)
	missingPath := filepath.Join(tmpDir, "missing.json")

	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{
		StructuresFile: map[string]string{
			"jp": validPath,
			"en": missingPath,
			"tw": "",
		},
	})

	loadedRegions, failures := service.LoadStatus()
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
	schemaPath := writeTestSuiteSchema(t, tmpDir)

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

func writeTestSuiteSchema(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "suite_user.avsc")
	if err := os.WriteFile(path, testStructToolSuiteSchema(), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
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

func TestSuiteRestoreServiceLoadStatusReturnsFailureMapCopy(t *testing.T) {
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{
		StructuresFile: map[string]string{
			"en": filepath.Join(t.TempDir(), "missing.json"),
		},
	})

	_, failures := service.LoadStatus()
	if len(failures) != 1 {
		t.Fatalf("len(failures) = %d, want %d", len(failures), 1)
	}
	failures["en"] = "mutated"

	_, failuresAgain := service.LoadStatus()
	if failuresAgain["en"] == "mutated" {
		t.Fatalf("LoadStatus should return a copy of failures map")
	}
}

func TestSuiteRestoreServiceDatabasePurposeCleansAndRespectsEnabledRegions(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := writeTestSuiteSchema(t, tmpDir)
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{
		StructuresFile:  map[string]string{"jp": schemaPath},
		EnableRegions:   []string{"jp"},
		SuiteRemoveKeys: []string{"removeMe"},
	})

	data := map[string]any{
		"removeMe":  []any{1},
		"userCards": []any{[]any{int64(100), int64(30)}},
	}
	restored, report, err := service.Restore(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeDatabase},
	)
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
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

func TestSuiteRestoreServiceDatabasePurposeCleansBeforeSkippingDisabledRegion(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := writeTestSuiteSchema(t, tmpDir)
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{
		StructuresFile:  map[string]string{"jp": schemaPath},
		EnableRegions:   []string{"en"},
		SuiteRemoveKeys: []string{"removeMe"},
	})

	data := map[string]any{
		"removeMe":  []any{1},
		"userCards": []any{[]any{int64(100), int64(30)}},
	}
	restored, report, err := service.Restore(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeDatabase},
	)
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if report.Enabled {
		t.Fatalf("database restore should be disabled for jp, report=%#v", report)
	}
	if len(restored["removeMe"].([]any)) != 0 {
		t.Fatalf("database purpose should clean before region gating, got %#v", restored["removeMe"])
	}
	if _, ok := restored["userCards"].([]any)[0].([]any); !ok {
		t.Fatalf("disabled region should keep compact array, got %#v", restored["userCards"])
	}
}

func TestSuiteRestoreServiceSyncPurposeIgnoresEnabledRegionsAndDoesNotClean(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := writeTestSuiteSchema(t, tmpDir)
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{
		StructuresFile:  map[string]string{"jp": schemaPath},
		EnableRegions:   []string{"en"},
		SuiteRemoveKeys: []string{"removeMe"},
	})

	data := map[string]any{
		"removeMe":  []any{1},
		"userCards": []any{[]any{int64(100), int64(30)}},
	}
	restored, report, err := service.Restore(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync},
	)
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
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

func TestSuiteRestoreServiceMissingRestorerReportsWithoutError(t *testing.T) {
	service := NewSuiteRestoreService(SuiteRestoreServiceOptions{})
	data := map[string]any{"userCards": []any{[]any{int64(100), int64(30)}}}
	restored, report, err := service.Restore(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync},
	)
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
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

func TestSuiteRestoreServiceDefensiveCopiesAndInstancesAreIsolated(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := writeTestSuiteSchema(t, tmpDir)
	structures := map[string]string{"jp": schemaPath}
	enabledRegions := []string{"jp"}
	removeKeys := []string{"removeMe"}

	first := NewSuiteRestoreService(SuiteRestoreServiceOptions{
		StructuresFile:  structures,
		EnableRegions:   enabledRegions,
		SuiteRemoveKeys: removeKeys,
	})
	structures["jp"] = filepath.Join(tmpDir, "missing-after-construction.avsc")
	structures["en"] = schemaPath
	enabledRegions[0] = "en"
	removeKeys[0] = "other"

	second := NewSuiteRestoreService(SuiteRestoreServiceOptions{
		StructuresFile:  map[string]string{},
		EnableRegions:   []string{"en"},
		SuiteRemoveKeys: []string{"other"},
	})

	data := map[string]any{
		"removeMe":  []any{1},
		"userCards": []any{[]any{int64(100), int64(30)}},
	}
	restored, report, err := first.Restore(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeDatabase},
	)
	if err != nil {
		t.Fatalf("first.Restore returned error: %v", err)
	}
	if !report.Enabled || !report.RestorerLoaded || report.Source != schemaPath {
		t.Fatalf("first service observed mutated constructor inputs: %#v", report)
	}
	if len(restored["removeMe"].([]any)) != 0 {
		t.Fatalf("first service observed mutated remove keys: %#v", restored["removeMe"])
	}

	firstLoaded, firstFailures := first.LoadStatus()
	secondLoaded, secondFailures := second.LoadStatus()
	if firstLoaded != 1 || len(firstFailures) != 0 {
		t.Fatalf("first status = (%d, %#v), want one loaded and no failures", firstLoaded, firstFailures)
	}
	if secondLoaded != 0 || len(secondFailures) != 0 {
		t.Fatalf("second status = (%d, %#v), want empty isolated service", secondLoaded, secondFailures)
	}
}

func TestNilSuiteRestoreServiceFailsClosed(t *testing.T) {
	var service *SuiteRestoreService
	data := map[string]any{"userCards": []any{}}
	if _, _, err := service.Restore(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync},
	); err == nil {
		t.Fatal("nil SuiteRestoreService should fail instead of silently skipping restoration")
	}

	loaded, failures := service.LoadStatus()
	if loaded != 0 || len(failures) != 1 {
		t.Fatalf("nil service status = (%d, %#v), want one degraded failure", loaded, failures)
	}
}

func TestZeroValueSuiteRestoreServiceFailsClosed(t *testing.T) {
	service := &SuiteRestoreService{}
	data := map[string]any{"userCards": []any{}}
	if _, _, err := service.Restore(
		harukiUtils.SupportedDataUploadServerJP,
		data,
		SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync},
	); err == nil {
		t.Fatal("zero-value SuiteRestoreService should fail instead of silently skipping restoration")
	}

	loaded, failures := service.LoadStatus()
	if loaded != 0 || len(failures) != 1 {
		t.Fatalf("zero-value service status = (%d, %#v), want one degraded failure", loaded, failures)
	}
}
