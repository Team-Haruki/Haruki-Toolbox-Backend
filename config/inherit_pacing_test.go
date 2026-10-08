package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadInheritPacing(t *testing.T, yaml string) SekaiInheritPacingConfig {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg.SekaiClient.InheritPacing
}

// The defaults are the pacing the inherit flow has always used: 1 s between
// the two inherit calls, 2 s before login, 1 s around each suite follow-up.
// Shortening them is an owner decision, so this test pins the values.
func TestInheritPacingDefaultsAreUnchanged(t *testing.T) {
	want := SekaiInheritPacingConfig{AfterInheritCheckMS: 1000, BeforeLoginMS: 2000, SuiteFollowupMS: 1000}
	if got := DefaultSekaiInheritPacingConfig(); got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	for name, yaml := range map[string]string{
		"no sekai_client block":   "backend:\n  port: 1\n",
		"no inherit_pacing block": "sekai_client:\n  jp_server_api_host: \"\"\n",
		"empty inherit_pacing":    "sekai_client:\n  inherit_pacing:\n",
		"zero and negative":       "sekai_client:\n  inherit_pacing:\n    after_inherit_check_ms: 0\n    before_login_ms: -1\n",
	} {
		if got := loadInheritPacing(t, yaml); got != want {
			t.Fatalf("%s: pacing = %+v, want defaults %+v", name, got, want)
		}
	}
}

// The example file documents every inherit_pacing key with its default.
func TestExampleConfigInheritPacingMatchesDefaults(t *testing.T) {
	cfg, err := Load("../haruki-toolbox-configs.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if got, want := cfg.SekaiClient.InheritPacing, DefaultSekaiInheritPacingConfig(); got != want {
		t.Fatalf("example sekai_client.inherit_pacing = %+v, want defaults %+v", got, want)
	}
}

func TestInheritPacingYAMLAndEnv(t *testing.T) {
	got := loadInheritPacing(t, "sekai_client:\n  inherit_pacing:\n    suite_followup_ms: 500\n")
	if want := (SekaiInheritPacingConfig{AfterInheritCheckMS: 1000, BeforeLoginMS: 2000, SuiteFollowupMS: 500}); got != want {
		t.Fatalf("partial block = %+v, want %+v", got, want)
	}

	t.Setenv("SEKAI_INHERIT_PAUSE_AFTER_CHECK_MS", "300")
	t.Setenv("SEKAI_INHERIT_PAUSE_BEFORE_LOGIN_MS", "400")
	t.Setenv("SEKAI_INHERIT_PAUSE_SUITE_FOLLOWUP_MS", "600")
	got = loadInheritPacing(t, "sekai_client:\n  inherit_pacing:\n    suite_followup_ms: 500\n")
	if want := (SekaiInheritPacingConfig{AfterInheritCheckMS: 300, BeforeLoginMS: 400, SuiteFollowupMS: 600}); got != want {
		t.Fatalf("env overrides = %+v, want %+v", got, want)
	}

	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("backend:\n  port: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"SEKAI_INHERIT_PAUSE_AFTER_CHECK_MS",
		"SEKAI_INHERIT_PAUSE_BEFORE_LOGIN_MS",
		"SEKAI_INHERIT_PAUSE_SUITE_FOLLOWUP_MS",
	} {
		t.Setenv(key, "2s")
		if _, err := Load(cfgPath); err == nil {
			t.Fatalf("a non-integer %s must fail", key)
		}
		t.Setenv(key, "100")
	}
}
