package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSekaiAPIBackupEndpointDefaultsAndEnv(t *testing.T) {
	unsetSekaiAPIBackupEndpoint(t)
	tmp := t.TempDir()
	emptyPath := filepath.Join(tmp, "empty.yaml")
	if err := os.WriteFile(emptyPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(emptyPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SekaiAPI.BackupEndpoint != "" {
		t.Fatalf("default backup_endpoint = %q, want empty (no failover)", cfg.SekaiAPI.BackupEndpoint)
	}

	yamlPath := filepath.Join(tmp, "cfg.yaml")
	content := []byte("sekai_api:\n  api_endpoint: http://primary.example\n  backup_endpoint: http://backup-from-yaml.example\n")
	if err := os.WriteFile(yamlPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(yamlPath); err != nil || cfg.SekaiAPI.BackupEndpoint != "http://backup-from-yaml.example" {
		t.Fatalf("yaml backup_endpoint = %q (err %v)", cfg.SekaiAPI.BackupEndpoint, err)
	}

	t.Setenv("SEKAI_API_BACKUP_ENDPOINT", " http://backup-from-env.example ")
	if cfg, err = Load(yamlPath); err != nil || cfg.SekaiAPI.BackupEndpoint != "http://backup-from-env.example" {
		t.Fatalf("env backup_endpoint = %q (err %v)", cfg.SekaiAPI.BackupEndpoint, err)
	}
	if cfg.SekaiAPI.APIEndpoint != "http://primary.example" {
		t.Fatalf("api_endpoint = %q", cfg.SekaiAPI.APIEndpoint)
	}
}

// The example config, .env.example and compose ship failover disabled, and
// compose passes the variable through.
func TestExampleConfigSekaiAPIBackupDisabled(t *testing.T) {
	unsetSekaiAPIBackupEndpoint(t)
	cfg, err := Load("../haruki-toolbox-configs.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if cfg.SekaiAPI.BackupEndpoint != "" {
		t.Fatalf("example backup_endpoint = %q, want empty", cfg.SekaiAPI.BackupEndpoint)
	}
	if value, ok := readEnvExample(t, "../.env.example")["SEKAI_API_BACKUP_ENDPOINT"]; !ok || value != "" {
		t.Fatalf(".env.example SEKAI_API_BACKUP_ENDPOINT = %q (present %v), want present and empty", value, ok)
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
	if got := compose.Services["backend"].Environment["SEKAI_API_BACKUP_ENDPOINT"]; got != "${SEKAI_API_BACKUP_ENDPOINT:-}" {
		t.Fatalf("compose backend SEKAI_API_BACKUP_ENDPOINT = %q, want ${SEKAI_API_BACKUP_ENDPOINT:-}", got)
	}
}

// unsetSekaiAPIBackupEndpoint removes the variable for the test and restores it
// afterwards (t.Setenv registers the restore).
func unsetSekaiAPIBackupEndpoint(t *testing.T) {
	t.Helper()
	t.Setenv("SEKAI_API_BACKUP_ENDPOINT", "")
	if err := os.Unsetenv("SEKAI_API_BACKUP_ENDPOINT"); err != nil {
		t.Fatal(err)
	}
}
