package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInternalUploadTokenFileDefaultsAndEnv(t *testing.T) {
	tmp := t.TempDir()
	emptyPath := filepath.Join(tmp, "empty.yaml")
	if err := os.WriteFile(emptyPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(emptyPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.InternalUpload.TokenSHA256File != "" {
		t.Fatalf("default token_sha256_file = %q, want empty (endpoint off)", cfg.InternalUpload.TokenSHA256File)
	}

	yamlPath := filepath.Join(tmp, "cfg.yaml")
	if err := os.WriteFile(yamlPath, []byte("internal_upload:\n  token_sha256_file: /path/from-yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(yamlPath); err != nil || cfg.InternalUpload.TokenSHA256File != "/path/from-yaml" {
		t.Fatalf("yaml token_sha256_file = %q (err %v)", cfg.InternalUpload.TokenSHA256File, err)
	}

	t.Setenv("INTERNAL_UPLOAD_TOKEN_SHA256_FILE", "/path/from-env")
	if cfg, err = Load(yamlPath); err != nil || cfg.InternalUpload.TokenSHA256File != "/path/from-env" {
		t.Fatalf("env token_sha256_file = %q (err %v)", cfg.InternalUpload.TokenSHA256File, err)
	}
}

// The example config ships the endpoint disabled.
func TestExampleConfigInternalUploadDisabled(t *testing.T) {
	t.Setenv("INTERNAL_UPLOAD_TOKEN_SHA256_FILE", "")
	cfg, err := Load("../haruki-toolbox-configs.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	if cfg.InternalUpload.TokenSHA256File != "" {
		t.Fatalf("example token_sha256_file = %q, want empty", cfg.InternalUpload.TokenSHA256File)
	}
}
