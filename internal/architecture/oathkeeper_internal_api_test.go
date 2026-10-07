package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestInternalAPINotRoutedByOathkeeper keeps /internal/ off the public
// gateway: POST /internal/oauth2/introspect, POST
// /internal/bot-security/alerts and the birthday-monitor mirror are reachable only on the backend port (private network / compose network,
// never via the public proxy), so no Oathkeeper access rule anywhere in the
// repository may match an /internal/ URL, for any method, scheme or host.
func TestInternalAPINotRoutedByOathkeeper(t *testing.T) {
	root := repositoryRoot(t)
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.Contains(name, "access-rules") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
	canonical := filepath.Join(root, "external", "oathkeeper", "access-rules.yml")
	if len(files) == 0 || !containsPath(files, canonical) {
		t.Fatalf("Oathkeeper access rules not found (got %v)", files)
	}

	probes := internalAPIProbeURLs()
	for _, file := range files {
		var rules []oathkeeperRule
		if file == canonical {
			rules = loadOathkeeperRules(t)
		} else {
			contents, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}
			if err := yaml.Unmarshal(contents, &rules); err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
		}
		if len(rules) == 0 {
			t.Fatalf("%s holds no rules", file)
		}
		for _, rule := range rules {
			if strings.Contains(rule.Match.URL, "/internal") {
				t.Errorf("%s: rule %q names /internal in its match URL %q", file, rule.ID, rule.Match.URL)
				continue
			}
			pattern := compileOathkeeperURL(t, rule.Match.URL)
			for _, probe := range probes {
				if pattern.MatchString(probe) {
					t.Errorf("%s: rule %q matches %s", file, rule.ID, probe)
				}
			}
		}
	}
}

func internalAPIProbeURLs() []string {
	hosts := []string{"toolbox-api-direct.haruki.seiunx.com", "haruki.seiunx.com", "toolbox.example.com", "backend:16666"}
	paths := []string{
		"/internal/",
		"/internal/oauth2/introspect",
		"/internal/oauth2/introspect/",
		"/internal/mysekai-birthday-monitors/s1",
		"/internal/mysekai-birthday-events/e1",
		"/internal/mysekai-birthday-events/e1/ack",
		"/internal/bot-security/alerts",
		"/internal/bot-security/alerts/",
	}
	var probes []string
	for _, scheme := range []string{"http", "https"} {
		for _, host := range hosts {
			for _, path := range paths {
				probes = append(probes, scheme+"://"+host+path)
			}
		}
	}
	return probes
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(want) {
			return true
		}
	}
	return false
}
