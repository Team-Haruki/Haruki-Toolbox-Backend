package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOryOIDCProviderGatewayContract(t *testing.T) {
	root := repositoryRoot(t)
	assertFileContainsAll(t, filepath.Join(root, "external", "hydra", "hydra.yml"),
		"issuer:",
		"supported_types:",
		"- public",
		"- pairwise",
	)
	assertFileContainsAll(t, filepath.Join(root, "external", "oathkeeper", "access-rules.yml"),
		"/.well-known/<.*>",
		"oauth2/jwks.json",
		"userinfo",
	)
}

func assertFileContainsAll(t *testing.T, path string, expected ...string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, fragment := range expected {
		if !strings.Contains(string(contents), fragment) {
			t.Errorf("%s is missing OIDC contract fragment %q", path, fragment)
		}
	}
}
