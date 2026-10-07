package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegrationDocGoSampleMatchesLiveTest keeps the Go example of the device
// authorization contract (docs/oauth2-integration.zh-CN.md §4A.8) identical to
// the code the live Hydra device-flow test runs against v25.4.0 and v26.2.0
// (GoXOAuth2Sample in internal/modules/oauth2/hydra_device_live_test.go). Each
// function, with its doc comment, must appear verbatim in the integration doc.
func TestIntegrationDocGoSampleMatchesLiveTest(t *testing.T) {
	root := repositoryRoot(t)
	liveTest := readRepositoryFile(t, filepath.Join(root, "internal", "modules", "oauth2", "hydra_device_live_test.go"))
	doc := readRepositoryFile(t, filepath.Join(root, "docs", "oauth2-integration.zh-CN.md"))
	for _, name := range []string{"deviceLogin", "authorizedAccountName", "retryAfter", "sleepContext"} {
		source := goFunctionWithComment(t, liveTest, name)
		if !strings.Contains(doc, source) {
			t.Errorf("docs/oauth2-integration.zh-CN.md §4A.8 does not contain func %s exactly as hydra_device_live_test.go has it", name)
		}
	}
}

func readRepositoryFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}

// goFunctionWithComment returns a top-level function from its doc comment to
// its closing brace, assuming gofmt layout.
func goFunctionWithComment(t *testing.T, source, name string) string {
	t.Helper()
	lines := strings.Split(source, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "func "+name+"(") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("func %s not found", name)
	}
	end := start
	for end < len(lines) && lines[end] != "}" {
		end++
	}
	if end == len(lines) {
		t.Fatalf("func %s has no closing brace", name)
	}
	first := start
	for first > 0 && strings.HasPrefix(lines[first-1], "//") {
		first--
	}
	return strings.Join(lines[first:end+1], "\n") + "\n"
}
