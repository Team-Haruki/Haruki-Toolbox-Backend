package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const testInternalAPITokenSHA256 = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func TestOAuth2InternalAPIDefaultsAndEnv(t *testing.T) {
	tmp := t.TempDir()
	emptyPath := filepath.Join(tmp, "empty.yaml")
	if err := os.WriteFile(emptyPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(emptyPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.OAuth2.InternalAPI.TokenSHA256 != "" {
		t.Fatalf("default token_sha256 = %q, want empty (internal API off)", cfg.OAuth2.InternalAPI.TokenSHA256)
	}

	yamlPath := filepath.Join(tmp, "cfg.yaml")
	content := []byte("oauth2:\n  internal_api:\n    token_sha256: \"" + testInternalAPITokenSHA256 + "\"\n")
	if err := os.WriteFile(yamlPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(yamlPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.OAuth2.InternalAPI.TokenSHA256 != testInternalAPITokenSHA256 {
		t.Fatalf("yaml token_sha256 = %q", cfg.OAuth2.InternalAPI.TokenSHA256)
	}

	override := "A665A45920422F9D417E4867EFDC4FB8A04A1F3FFF1FA07E998E86F7F7A27AE3"
	t.Setenv("OAUTH2_INTERNAL_API_TOKEN_SHA256", override)
	cfg, err = Load(yamlPath)
	if err != nil {
		t.Fatalf("Load with env returned error: %v", err)
	}
	if cfg.OAuth2.InternalAPI.TokenSHA256 != override {
		t.Fatalf("env token_sha256 = %q, want %q", cfg.OAuth2.InternalAPI.TokenSHA256, override)
	}
}

// The example config ships the internal API disabled.
func TestExampleConfigInternalAPIDisabled(t *testing.T) {
	cfg, err := Load("../haruki-toolbox-configs.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if cfg.OAuth2.InternalAPI.TokenSHA256 != "" {
		t.Fatalf("example token_sha256 = %q, want empty", cfg.OAuth2.InternalAPI.TokenSHA256)
	}
	if cfg.OAuth2.InternalAPI.ClientID != "station" || !slices.Equal(cfg.OAuth2.InternalAPI.Audience, []string{"station"}) {
		t.Fatalf("example client_id %q audience %q, want station [station]", cfg.OAuth2.InternalAPI.ClientID, cfg.OAuth2.InternalAPI.Audience)
	}
}

func TestOAuth2InternalAPICallerDefaultsAndEnv(t *testing.T) {
	tmp := t.TempDir()
	load := func(content string) Config {
		t.Helper()
		path := filepath.Join(tmp, "cfg.yaml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		return cfg
	}
	assertCaller := func(cfg Config, clientID string, audience []string) {
		t.Helper()
		if cfg.OAuth2.InternalAPI.ClientID != clientID || !slices.Equal(cfg.OAuth2.InternalAPI.Audience, audience) {
			t.Fatalf("client_id %q audience %q, want %q %q", cfg.OAuth2.InternalAPI.ClientID, cfg.OAuth2.InternalAPI.Audience, clientID, audience)
		}
	}

	assertCaller(load("{}\n"), "station", []string{"station"})
	// Blank values fall back to the defaults.
	assertCaller(load("oauth2:\n  internal_api:\n    client_id: \"  \"\n    audience: []\n"), "station", []string{"station"})
	assertCaller(load("oauth2:\n  internal_api:\n    client_id: station-staging\n    audience: [station, station-staging]\n"), "station-staging", []string{"station", "station-staging"})

	t.Setenv("OAUTH2_INTERNAL_API_CLIENT_ID", "station-env")
	t.Setenv("OAUTH2_INTERNAL_API_AUDIENCE", " station-a , ,station-b ")
	assertCaller(load("oauth2:\n  internal_api:\n    client_id: station-staging\n"), "station-env", []string{"station-a", "station-b"})

	// The defaults are fresh slices: one config cannot change another's.
	first := load("{}\n")
	first.OAuth2.InternalAPI.Audience[0] = "mutated"
	t.Setenv("OAUTH2_INTERNAL_API_CLIENT_ID", "")
	t.Setenv("OAUTH2_INTERNAL_API_AUDIENCE", "")
	assertCaller(load("{}\n"), "station", []string{"station"})
}
