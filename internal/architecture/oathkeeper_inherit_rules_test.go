package architecture

import (
	"regexp"
	"slices"
	"testing"
)

// Every inherit job and vault route needs a session; only the retired
// synchronous submit route stays public (it only answers 410). Each path and
// method must match exactly one rule.
func TestOathkeeperInheritRules(t *testing.T) {
	rules := loadOathkeeperRules(t)
	compiled := make([]*regexp.Regexp, len(rules))
	for i, rule := range rules {
		compiled[i] = compileOathkeeperURL(t, rule.Match.URL)
	}
	const base = "https://toolbox.example.com/api/inherit/"
	cases := []struct {
		method, path, want string
	}{
		{"POST", "jp/suite/submit", "haruki-public-upload-entry"},
		{"POST", "en/mysekai/submit", "haruki-public-upload-entry"},
		{"POST", "jobs", "haruki-protected-inherit-jobs"},
		{"GET", "jobs/0b6c1f5e-0000-4000-8000-000000000000", "haruki-protected-inherit-jobs"},
		{"POST", "jobs/0b6c1f5e-0000-4000-8000-000000000000/confirm", "haruki-protected-inherit-jobs"},
		{"POST", "jobs/0b6c1f5e-0000-4000-8000-000000000000/cancel", "haruki-protected-inherit-jobs"},
		{"POST", "jobs/0b6c1f5e-0000-4000-8000-000000000000/ws-ticket", "haruki-protected-inherit-jobs"},
		{"GET", "jobs/0b6c1f5e-0000-4000-8000-000000000000/ws", "haruki-protected-inherit-job-ws"},
		{"GET", "vault/key", "haruki-protected-inherit-vault"},
		{"POST", "vault/key/reset", "haruki-protected-inherit-vault"},
		{"GET", "vault/entries", "haruki-protected-inherit-vault"},
		{"POST", "vault/entries/e1/reveal", "haruki-protected-inherit-vault"},
		{"POST", "vault/entries/e1/reissue", "haruki-protected-inherit-vault"},
		{"DELETE", "vault/entries/e1", "haruki-protected-inherit-vault"},
		{"POST", "tw/suite/submit", ""},
		{"POST", "jobs/x/submit", ""},
		{"GET", "jobs/x/ws/extra", ""},
		{"POST", "vault/entries/e1/export", ""},
	}
	for _, tc := range cases {
		var got []oathkeeperRule
		for i, rule := range rules {
			if slices.Contains(rule.Match.Methods, tc.method) && compiled[i].MatchString(base+tc.path) {
				got = append(got, rule)
			}
		}
		switch {
		case tc.want == "" && len(got) != 0:
			t.Errorf("%s %s: expected no rule, matched %q", tc.method, tc.path, got[0].ID)
		case tc.want != "" && (len(got) != 1 || got[0].ID != tc.want):
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID
			}
			t.Errorf("%s %s: expected exactly %q, matched %v", tc.method, tc.path, tc.want, ids)
		}
	}
	for _, rule := range rules {
		switch rule.ID {
		case "haruki-protected-inherit-jobs", "haruki-protected-inherit-job-ws", "haruki-protected-inherit-vault":
			if len(rule.Authenticators) != 1 || rule.Authenticators[0].Handler != "cookie_session" {
				t.Errorf("%s must use cookie_session, got %+v", rule.ID, rule.Authenticators)
			}
			if len(rule.Mutators) != 1 || rule.Mutators[0].Handler != "header" {
				t.Errorf("%s must inject the trusted headers, got %+v", rule.ID, rule.Mutators)
			}
		}
	}
}
