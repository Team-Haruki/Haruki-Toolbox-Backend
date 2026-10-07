package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBotSecurityIngestTokenDefaultsAndEnv(t *testing.T) {
	tmp := t.TempDir()
	emptyPath := filepath.Join(tmp, "empty.yaml")
	if err := os.WriteFile(emptyPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(emptyPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.BotSecurity.IngestTokenSHA256 != "" {
		t.Fatalf("default ingest_token_sha256 = %q, want empty (ingest off)", cfg.BotSecurity.IngestTokenSHA256)
	}

	yamlPath := filepath.Join(tmp, "cfg.yaml")
	content := []byte("bot_security:\n  ingest_token_sha256: \"" + testInternalAPITokenSHA256 + "\"\n")
	if err := os.WriteFile(yamlPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(yamlPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.BotSecurity.IngestTokenSHA256 != testInternalAPITokenSHA256 {
		t.Fatalf("yaml ingest_token_sha256 = %q", cfg.BotSecurity.IngestTokenSHA256)
	}

	override := "A665A45920422F9D417E4867EFDC4FB8A04A1F3FFF1FA07E998E86F7F7A27AE3"
	t.Setenv("BOT_SECURITY_INGEST_TOKEN_SHA256", override)
	cfg, err = Load(yamlPath)
	if err != nil {
		t.Fatalf("Load with env returned error: %v", err)
	}
	if cfg.BotSecurity.IngestTokenSHA256 != override {
		t.Fatalf("env ingest_token_sha256 = %q, want %q", cfg.BotSecurity.IngestTokenSHA256, override)
	}
}

// The example config, .env.example and compose ship ingestion disabled.
func TestExampleConfigBotSecurityDisabled(t *testing.T) {
	cfg, err := Load("../haruki-toolbox-configs.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if cfg.BotSecurity.IngestTokenSHA256 != "" {
		t.Fatalf("example ingest_token_sha256 = %q, want empty", cfg.BotSecurity.IngestTokenSHA256)
	}
	if value, ok := readEnvExample(t, "../.env.example")["BOT_SECURITY_INGEST_TOKEN_SHA256"]; !ok || value != "" {
		t.Fatalf(".env.example BOT_SECURITY_INGEST_TOKEN_SHA256 = %q (present %v), want present and empty", value, ok)
	}

	contents, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	var compose struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(contents, &compose); err != nil {
		t.Fatalf("parse compose: %v", err)
	}
	if got := compose.Services["backend"].Environment["BOT_SECURITY_INGEST_TOKEN_SHA256"]; got != "${BOT_SECURITY_INGEST_TOKEN_SHA256:-}" {
		t.Fatalf("compose backend BOT_SECURITY_INGEST_TOKEN_SHA256 = %q, want ${BOT_SECURITY_INGEST_TOKEN_SHA256:-}", got)
	}
}
