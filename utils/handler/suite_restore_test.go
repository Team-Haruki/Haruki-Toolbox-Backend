package handler

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	harukiConfig "haruki-suite/config"
)

func resetSuiteRestorerStateForTest() {
	suiteRestorerOnce = sync.Once{}
	suiteRestorerMap = nil
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
	validPath := filepath.Join(tmpDir, "suite.json")
	if err := os.WriteFile(validPath, []byte(`{"userGamedata":["id"]}`), 0600); err != nil {
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
