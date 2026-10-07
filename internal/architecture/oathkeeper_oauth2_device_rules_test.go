package architecture

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The device authorization grant (RFC 8628) is backend-mediated: devices call
// the backend's anonymous POST /api/oauth2/device/auth and poll
// /api/oauth2/token, the /device page calls the session-guarded
// lookup/approve/deny, and Hydra's own device endpoints are never routed.
// These tests pin that gateway contract (docs/oauth2-device-flow-design §10.5).

const (
	backendUpstream        = "http://backend:16666"
	publicOAuthProxyRuleID = "haruki-public-oauth-proxy"
	oauthConsentRuleID     = "haruki-protected-oauth-consent"
	protectedUserRuleID    = "haruki-protected-user"
	protectedUserGetRuleID = "haruki-protected-user-get"
	hydraPublicOpenIDRule  = "hydra-public-openid"
	hydraPublicOAuthRuleID = "hydra-public-oauth"
)

var oathkeeperProbeMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

var oathkeeperProbeBases = []string{
	"https://toolbox-api-direct.haruki.seiunx.com",
	"http://toolbox-api-direct.haruki.seiunx.com",
	"https://toolbox.example.com",
}

type compiledOathkeeperRule struct {
	rule    oathkeeperRule
	pattern *regexp.Regexp
}

func loadCompiledOathkeeperRules(t *testing.T) []compiledOathkeeperRule {
	t.Helper()
	rules := loadOathkeeperRules(t)
	if len(rules) == 0 {
		t.Fatal("no Oathkeeper rules loaded")
	}
	compiled := make([]compiledOathkeeperRule, len(rules))
	for i, rule := range rules {
		compiled[i] = compiledOathkeeperRule{rule: rule, pattern: compileOathkeeperURL(t, rule.Match.URL)}
	}
	return compiled
}

// matchingOathkeeperRules returns every rule Oathkeeper would consider for the
// method and URL: the rule must list the method and its URL pattern must match
// the whole URL.
func matchingOathkeeperRules(rules []compiledOathkeeperRule, method, url string) []oathkeeperRule {
	var out []oathkeeperRule
	for _, candidate := range rules {
		if slices.Contains(candidate.rule.Match.Methods, method) && candidate.pattern.MatchString(url) {
			out = append(out, candidate.rule)
		}
	}
	return out
}

func oathkeeperRuleIDs(rules []oathkeeperRule) []string {
	ids := make([]string, len(rules))
	for i, rule := range rules {
		ids[i] = rule.ID
	}
	return ids
}

func authenticatorHandlers(rule oathkeeperRule) []string {
	handlers := make([]string, len(rule.Authenticators))
	for i, a := range rule.Authenticators {
		handlers[i] = a.Handler
	}
	return handlers
}

func mutatorHandlers(rule oathkeeperRule) []string {
	handlers := make([]string, len(rule.Mutators))
	for i, m := range rule.Mutators {
		handlers[i] = m.Handler
	}
	return handlers
}

func isAnonymousRule(rule oathkeeperRule) bool {
	for _, handler := range authenticatorHandlers(rule) {
		if handler == "noop" || handler == "anonymous" || handler == "unauthorized" {
			return true
		}
	}
	return len(rule.Authenticators) == 0
}

// assertSingleRule checks that exactly the named rule serves method+url, and
// that it forwards to the backend unmodified (no strip_path).
func assertSingleRule(t *testing.T, rules []compiledOathkeeperRule, method, url, wantID string) oathkeeperRule {
	t.Helper()
	got := matchingOathkeeperRules(rules, method, url)
	if len(got) != 1 || got[0].ID != wantID {
		t.Errorf("%s %s: expected exactly rule %q, matched %v", method, url, wantID, oathkeeperRuleIDs(got))
		return oathkeeperRule{}
	}
	return got[0]
}

func assertBackendUpstream(t *testing.T, rule oathkeeperRule) {
	t.Helper()
	if rule.Upstream.URL != backendUpstream || rule.Upstream.StripPath != "" {
		t.Errorf("rule %q: upstream = %+v, want %s without strip_path", rule.ID, rule.Upstream, backendUpstream)
	}
}

func assertCookieSessionWithHeaders(t *testing.T, rule oathkeeperRule) {
	t.Helper()
	if got := authenticatorHandlers(rule); !slices.Equal(got, []string{"cookie_session"}) {
		t.Errorf("rule %q: authenticators = %v, want [cookie_session]", rule.ID, got)
	}
	if got := mutatorHandlers(rule); !slices.Contains(got, "header") {
		t.Errorf("rule %q: mutators = %v, want the header mutator (trusted identity headers)", rule.ID, got)
	}
}

// TestDeviceAuthRoutesToPublicProxyOnly: POST /api/oauth2/device/auth is
// anonymous and served by the backend's public OAuth proxy rule, the token
// endpoint stays on that same rule, and no other /api/oauth2/device/* path is
// reachable anonymously.
func TestDeviceAuthRoutesToPublicProxyOnly(t *testing.T) {
	rules := loadCompiledOathkeeperRules(t)
	for _, base := range oathkeeperProbeBases {
		for _, path := range []string{"/api/oauth2/device/auth", "/api/oauth2/token"} {
			rule := assertSingleRule(t, rules, "POST", base+path, publicOAuthProxyRuleID)
			if rule.ID == "" {
				continue
			}
			assertBackendUpstream(t, rule)
			if got := authenticatorHandlers(rule); !slices.Equal(got, []string{"noop"}) {
				t.Errorf("rule %q: authenticators = %v, want [noop]", rule.ID, got)
			}
		}
		// Whatever the method, device/auth never reaches anything but the
		// public proxy rule (in particular never Hydra).
		for _, method := range oathkeeperProbeMethods {
			for _, rule := range matchingOathkeeperRules(rules, method, base+"/api/oauth2/device/auth") {
				if rule.ID != publicOAuthProxyRuleID {
					t.Errorf("%s %s/api/oauth2/device/auth also matches rule %q", method, base, rule.ID)
				}
			}
		}
		// Only device/auth is anonymous under /api/oauth2/device/.
		for _, path := range []string{
			"/api/oauth2/device",
			"/api/oauth2/device/",
			"/api/oauth2/device/auth/",
			"/api/oauth2/device/auth/x",
			"/api/oauth2/device/lookup",
			"/api/oauth2/device/approve",
			"/api/oauth2/device/deny",
			"/api/oauth2/device/verify",
			"/api/oauth2/device/token",
		} {
			for _, method := range oathkeeperProbeMethods {
				for _, rule := range matchingOathkeeperRules(rules, method, base+path) {
					if isAnonymousRule(rule) {
						t.Errorf("%s %s%s is anonymous through rule %q", method, base, path, rule.ID)
					}
				}
			}
		}
	}
}

// TestDeviceDecisionRoutesRequireCookieSession: the /device page's
// lookup/approve/deny go through the consent rule (Kratos cookie session plus
// the trusted-header mutator), and no method reaches them anonymously.
func TestDeviceDecisionRoutesRequireCookieSession(t *testing.T) {
	rules := loadCompiledOathkeeperRules(t)
	for _, base := range oathkeeperProbeBases {
		for _, action := range []string{"lookup", "approve", "deny"} {
			url := base + "/api/oauth2/device/" + action
			rule := assertSingleRule(t, rules, "POST", url, oauthConsentRuleID)
			if rule.ID != "" {
				assertBackendUpstream(t, rule)
				assertCookieSessionWithHeaders(t, rule)
			}
			for _, method := range oathkeeperProbeMethods {
				for _, matched := range matchingOathkeeperRules(rules, method, url) {
					if isAnonymousRule(matched) {
						t.Errorf("%s %s is anonymous through rule %q", method, url, matched.ID)
					}
				}
			}
		}
	}
}

// TestHydraDeviceEndpointsAreNotRouted: Hydra's device authorization, verify
// and fallback endpoints answer Oathkeeper's 404 for every method, while the
// discovery documents (which advertise the backend endpoints) and Hydra's own
// token endpoint stay routed.
func TestHydraDeviceEndpointsAreNotRouted(t *testing.T) {
	rules := loadCompiledOathkeeperRules(t)
	for _, base := range oathkeeperProbeBases {
		for _, path := range []string{
			"/oauth2/device",
			"/oauth2/device/",
			"/oauth2/device/auth",
			"/oauth2/device/auth/",
			"/oauth2/device/verify",
			"/oauth2/device/verify/",
			"/oauth2/fallbacks/device",
			"/oauth2/fallbacks/device/",
		} {
			for _, method := range oathkeeperProbeMethods {
				if got := matchingOathkeeperRules(rules, method, base+path); len(got) != 0 {
					t.Errorf("%s %s%s must not be routed, matched %v", method, base, path, oathkeeperRuleIDs(got))
				}
			}
		}
		for _, path := range []string{"/.well-known/openid-configuration", "/.well-known/oauth-authorization-server"} {
			assertSingleRule(t, rules, "GET", base+path, hydraPublicOpenIDRule)
		}
		assertSingleRule(t, rules, "POST", base+"/oauth2/token", hydraPublicOAuthRuleID)
	}
	for _, candidate := range rules {
		if strings.Contains(candidate.rule.Match.URL, "fallbacks") {
			t.Errorf("rule %q names Hydra fallbacks in %q", candidate.rule.ID, candidate.rule.Match.URL)
		}
	}
}

// TestPerDeviceRevokeRouteCovered: revoking one device's authorization
// (DELETE /api/user/:toolbox_user_id/oauth2/authorizations/:client_id/consents/:consent_request_id)
// is covered by the existing session-guarded user rule, with no new rule. The
// route manifest check extends this to every authorization route the backend
// registers, so new ones cannot land without gateway coverage.
func TestPerDeviceRevokeRouteCovered(t *testing.T) {
	rules := loadCompiledOathkeeperRules(t)
	const (
		userID    = "1234567890"
		clientID  = "haruki-client"
		consentID = "8c9a1f0e3b2d4c5a9e7f6d1b2a3c4e5f"
	)
	for _, base := range oathkeeperProbeBases {
		authorizations := base + "/api/user/" + userID + "/oauth2/authorizations"
		perDevice := authorizations + "/" + clientID + "/consents/" + consentID
		for _, url := range []string{perDevice, authorizations + "/" + clientID} {
			rule := assertSingleRule(t, rules, "DELETE", url, protectedUserRuleID)
			if rule.ID != "" {
				assertBackendUpstream(t, rule)
				assertCookieSessionWithHeaders(t, rule)
			}
		}
		if rule := assertSingleRule(t, rules, "GET", authorizations, protectedUserGetRuleID); rule.ID != "" {
			assertCookieSessionWithHeaders(t, rule)
		}
		for _, method := range oathkeeperProbeMethods {
			for _, matched := range matchingOathkeeperRules(rules, method, perDevice) {
				if isAnonymousRule(matched) {
					t.Errorf("%s %s is anonymous through rule %q", method, perDevice, matched.ID)
				}
			}
		}
	}

	// Every registered per-user authorization route is reachable only through
	// exactly one session-guarded backend rule.
	for _, route := range registeredRoutes(t) {
		if !strings.HasPrefix(route.path, "/api/user/:toolbox_user_id/oauth2/authorizations") {
			continue
		}
		url := "https://toolbox-api-direct.haruki.seiunx.com" + fillRouteParams(route.path)
		got := matchingOathkeeperRules(rules, route.method, url)
		if len(got) != 1 {
			t.Errorf("%s %s: expected exactly one Oathkeeper rule, matched %v", route.method, route.path, oathkeeperRuleIDs(got))
			continue
		}
		assertBackendUpstream(t, got[0])
		assertCookieSessionWithHeaders(t, got[0])
	}
}

type registeredRoute struct {
	method string
	path   string
}

// registeredRoutes reads the route manifest (api/testdata/routes.golden,
// "METHOD /path" per line).
func registeredRoutes(t *testing.T) []registeredRoute {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "api", "testdata", "routes.golden")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	var routes []registeredRoute
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		method, routePath, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !ok {
			continue
		}
		routes = append(routes, registeredRoute{method: method, path: routePath})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(routes) == 0 {
		t.Fatalf("%s lists no routes", path)
	}
	return routes
}

// fillRouteParams replaces Fiber ":param" segments with sample values.
func fillRouteParams(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[i] = "sample-" + strings.TrimPrefix(segment, ":")
		}
	}
	return strings.Join(segments, "/")
}
