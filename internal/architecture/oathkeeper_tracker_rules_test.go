package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type oathkeeperRule struct {
	ID    string `yaml:"id"`
	Match struct {
		URL     string   `yaml:"url"`
		Methods []string `yaml:"methods"`
	} `yaml:"match"`
	Authenticators []struct {
		Handler string `yaml:"handler"`
	} `yaml:"authenticators"`
}

// compileOathkeeperURL mirrors Oathkeeper's "regexp" matching strategy: text
// inside <...> is a regular expression group, everything else is literal, and
// the whole URL (scheme://host/path, no query string) must match.
func compileOathkeeperURL(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	var b strings.Builder
	b.WriteString("^")
	rest := pattern
	for {
		start := strings.IndexByte(rest, '<')
		if start < 0 {
			b.WriteString(regexp.QuoteMeta(rest))
			break
		}
		end := strings.IndexByte(rest[start:], '>')
		if end < 0 {
			t.Fatalf("unbalanced delimiters in %q", pattern)
		}
		b.WriteString(regexp.QuoteMeta(rest[:start]))
		b.WriteString("(" + rest[start+1:start+end] + ")")
		rest = rest[start+end+1:]
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func loadOathkeeperRules(t *testing.T) []oathkeeperRule {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "external", "oathkeeper", "access-rules.yml")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var rules []oathkeeperRule
	if err := yaml.Unmarshal(contents, &rules); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return rules
}

func TestOathkeeperEventTrackerWebRules(t *testing.T) {
	rules := loadOathkeeperRules(t)
	compiled := make([]*regexp.Regexp, len(rules))
	for i, rule := range rules {
		compiled[i] = compileOathkeeperURL(t, rule.Match.URL)
	}
	matching := func(url string) []oathkeeperRule {
		var out []oathkeeperRule
		for i, rule := range rules {
			if slices.Contains(rule.Match.Methods, "GET") && compiled[i].MatchString(url) {
				out = append(out, rule)
			}
		}
		return out
	}

	const (
		base      = "https://toolbox.example.com/event-tracker/api/v2/web/events/jp/150/leaderboards/"
		publicID  = "haruki-public-tracker-web-v2-prefixed"
		privateID = "haruki-protected-tracker-web-v2-private-prefixed"
	)
	cases := []struct {
		path string
		want string // "" means no rule may match
	}{
		{"total/overview", publicID},
		{"total/replay/overview", publicID},
		{"total/details/rank/100", publicID},
		{"total/details/user/abc", publicID},
		{"total/users/search", publicID},
		{"total/check-room", publicID},
		{"total/top100", publicID},
		{"total/borders", publicID},
		{"total/growth", publicID},
		{"total/status", publicID},
		{"world-bloom/21/overview", publicID},
		{"world-bloom/21/top100", publicID},
		{"world-bloom/21/borders", publicID},
		{"world-bloom/21/growth", publicID},
		{"world-bloom/21/status", publicID},
		{"world-bloom/21/check-room", publicID},
		{"total/private/details/user/123", privateID},
		{"world-bloom/21/private/details/user/123", privateID},
		{"total/top1000", ""},
		{"total/status/extra", ""},
		{"total/sk/status", ""},
		{"total/top100/extra", ""},
		{"world-bloom/top100", ""},
		{"world-bloom/21/extra/top100", ""},
	}
	for _, tc := range cases {
		got := matching(base + tc.path)
		switch {
		case tc.want == "" && len(got) != 0:
			t.Errorf("%s: expected no rule, matched %q", tc.path, got[0].ID)
		case tc.want != "" && len(got) != 1:
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID
			}
			t.Errorf("%s: expected exactly rule %q, matched %v", tc.path, tc.want, ids)
		case tc.want != "" && got[0].ID != tc.want:
			t.Errorf("%s: expected rule %q, matched %q", tc.path, tc.want, got[0].ID)
		}
	}

	for _, rule := range rules {
		if rule.ID != privateID {
			continue
		}
		if len(rule.Authenticators) == 0 || rule.Authenticators[0].Handler == "noop" || rule.Authenticators[0].Handler == "anonymous" {
			t.Errorf("%s must stay authenticated, got %+v", privateID, rule.Authenticators)
		}
	}
}
