package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// The token file only gates its own route: an unset, missing or malformed
// file leaves the endpoint off without stopping the backend, and the log
// never shows the file's content.
func TestNewInternalUploadConfig(t *testing.T) {
	sum := sha256.Sum256([]byte("bootstrap-internal-upload-token"))
	digest := hex.EncodeToString(sum[:])
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid")
	if err := os.WriteFile(valid, []byte(digest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	malformed := filepath.Join(dir, "malformed")
	if err := os.WriteFile(malformed, []byte("raw-secret-token-value"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		path    string
		enabled bool
		logged  string
	}{
		"unset":     {"", false, ""},
		"valid":     {valid, true, "Internal upload enabled"},
		"missing":   {filepath.Join(dir, "absent"), false, "Internal upload disabled"},
		"malformed": {malformed, false, "Internal upload disabled"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			logger := harukiLogger.NewLogger("InternalUpload", "DEBUG", &out)
			var cfg harukiConfig.Config
			cfg.InternalUpload.TokenSHA256File = tc.path
			got := newInternalUploadConfig(cfg, logger)
			if got.Enabled() != tc.enabled {
				t.Fatalf("enabled = %v, want %v", got.Enabled(), tc.enabled)
			}
			if tc.logged != "" && !strings.Contains(out.String(), tc.logged) {
				t.Fatalf("log %q does not mention %q", out.String(), tc.logged)
			}
			if strings.Contains(out.String(), "raw-secret-token-value") || strings.Contains(out.String(), digest) {
				t.Fatalf("log repeats the token file content: %q", out.String())
			}
		})
	}
}
