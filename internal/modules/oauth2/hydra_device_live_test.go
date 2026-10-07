//go:build hydra_live

// Live integration test of the OAuth2 device authorization grant (RFC 8628)
// against a real, non-dev Ory Hydra on Postgres. It is the gate for changing
// ORY_VERSION (docs/ory-suite-usage.zh-CN.md §10.6.6, recipe in §11.3): it
// re-proves the Hydra behaviour the backend compensates for (ory-suite-usage
// §10.5.1, cited as §10.5.1 below) and runs the backend's device flow end to
// end on top of it.
//
// The backend runs in process with the production route table
// (api.RegisterRoutes): the auth-proxy session guard fed Oathkeeper's headers,
// the runtime switch, the admin client routes, per-device revocation, the
// Bearer-protected profile, the internal introspection API and the reaper.
// Redis is miniredis and the user database is SQLite; only Hydra and its
// Postgres are real.
//
// Start Hydra with external/hydra/it/docker-compose.device-it.yml, then:
//
//	go test -tags hydra_live -count=1 -run TestHydraDeviceFlowLive -v ./internal/modules/oauth2
//
// The HYDRA_IT_* variables override the compose defaults (see liveEnv below);
// HYDRA_IT_VERSION, when set, must match the running Hydra's version.
package oauth2_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/api"
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	json "encoding/json/v2"
	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

const (
	liveAuthProxySecret  = "hydra-live-auth-proxy-secret-0123456789"
	liveSessionHeader    = "X-Auth-Proxy-Session-Id"
	liveSessionSignToken = "hydra-live-session-sign-token-0123456789"
	liveUserCodeCharset  = "BCDFGHJKLMNPQRSTVWXZ"
	// The reaper runs every second and handles flows 15 s past their expiry
	// (production: 60 s and 60 s), so ApprovedThenExpiredIsReaped can poll
	// between Hydra's expiry and the reaper.
	liveReaperInterval = time.Second
	liveReaperGrace    = 15 * time.Second
	// liveClientCacheTTL is the backend's in-process client cache (5 s) plus
	// a margin: a disabled client is refused once it has expired.
	liveClientCacheTTL = 5500 * time.Millisecond
	deviceGrantType    = oauth2Module.HydraGrantTypeDeviceCode
)

var (
	liveUserCodePattern = regexp.MustCompile(`^[` + liveUserCodeCharset + `]{4}-[` + liveUserCodeCharset + `]{4}$`)
	liveComposeDefault  = regexp.MustCompile(`^\$\{[A-Z0-9_]+:-(.*)\}$`)
)

// liveEnv reads a HYDRA_IT_* variable; the defaults are those of
// external/hydra/it/docker-compose.device-it.yml.
func liveEnv(name string) string {
	defaults := map[string]string{
		"HYDRA_IT_PUBLIC_URL":    "http://127.0.0.1:14444",
		"HYDRA_IT_ADMIN_URL":     "http://127.0.0.1:14445",
		"HYDRA_IT_DSN":           "postgres://hydra:hydra-it-password@127.0.0.1:15432/hydra?sslmode=disable",
		"HYDRA_IT_ISSUER_URL":    "https://toolbox-api.haruki-it.test",
		"HYDRA_IT_FRONTEND_URL":  "https://haruki.haruki-it.test",
		"HYDRA_IT_USER_CODE_TTL": "1m",
	}
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return defaults[name]
}

func TestHydraDeviceFlowLive(t *testing.T) {
	hydra := connectLiveHydra(t)
	t.Logf("Hydra %s at %s (user codes live %s)", hydra.version, hydra.publicURL, hydra.ttl)

	subtests := []struct {
		name string
		run  func(*testing.T, *liveHydra)
	}{
		{"PublicClientHappyPath", livePublicClientHappyPath},
		{"ConfidentialBasicOnlyClientID", liveConfidentialBasicOnlyClientID},
		{"TwoIdentitiesSingleWinner", liveTwoIdentitiesSingleWinner},
		{"DenyAccessDenied", liveDenyAccessDenied},
		{"ExpiryExpiredToken", liveExpiryExpiredToken},
		{"SlowDown", liveSlowDown},
		{"HydraDirectTokenRejectsWrappedCode", liveHydraDirectTokenRejectsWrappedCode},
		{"PerDeviceRevokeKillsRefreshedATandRT", livePerDeviceRevokeKillsRefreshedATandRT},
		{"DisabledClientBlocked", liveDisabledClientBlocked},
		{"DiscoveryAdvertisesBackendEndpoints", liveDiscoveryAdvertisesBackendEndpoints},
		{"ReaperRevokesUnredeemed", liveReaperRevokesUnredeemed},
		{"ApprovedThenExpiredIsReaped", liveApprovedThenExpiredIsReaped},
		{"JanitorSQLDeletesOnlyExpired", liveJanitorSQLDeletesOnlyExpired},
		{"GoXOAuth2Sample", liveGoXOAuth2Sample},
		{"StationScopeInternalIntrospect", liveStationScopeInternalIntrospect},
	}
	for _, subtest := range subtests {
		t.Run(subtest.name, func(t *testing.T) {
			// Every subtest has its own backend, clients and users; running
			// them in parallel overlaps the expiry waits.
			t.Parallel()
			subtest.run(t, hydra)
		})
	}
}

// --- Subtests -----------------------------------------------------------

func livePublicClientHappyPath(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"openid", "profile", "offline_access", "user:read"}})

	// Hydra's own answer (§10.5.1): form only, a stray "Header" member, an
	// ungrouped user code, Hydra's verification URI.
	raw := h.deviceAuthDirect(t, client, "user:read")
	if _, ok := raw["Header"]; !ok {
		t.Errorf("§10.5.1: Hydra device/auth no longer serializes a Header member: %v", raw)
	}
	if raw["verification_uri"] != h.issuerURL+"/oauth2/device/verify" || !strings.HasPrefix(stringField(raw, "device_code"), "ory_dc_") {
		t.Errorf("§10.5.1: unexpected Hydra device/auth answer %v", raw)
	}
	if code := stringField(raw, "user_code"); len(code) != 8 || strings.Trim(code, liveUserCodeCharset) != "" {
		t.Errorf("§10.5.1: Hydra user code %q is not 8 characters of the configured charset", code)
	}

	label := "Haruki-Client @ live-host"
	flow, resp := b.deviceAuth(client, "openid offline_access user:read", label)
	assertNoStore(t, resp)
	body := resp.json(t)
	wantKeys := []string{"device_code", "expires_in", "interval", "user_code", "verification_uri", "verification_uri_complete"}
	if keys := sortedKeys(body); !slices.Equal(keys, wantKeys) {
		t.Errorf("device/auth members = %v, want %v", keys, wantKeys)
	}
	if !strings.HasPrefix(flow.deviceCode, "hdc_") || len(flow.deviceCode) != 47 {
		t.Errorf("device_code %q is not a wrapped hdc_ code", flow.deviceCode)
	}
	if !liveUserCodePattern.MatchString(flow.userCode) {
		t.Errorf("user_code %q is not grouped XXXX-XXXX", flow.userCode)
	}
	if flow.verificationURI != h.frontendURL+"/device" || flow.verificationURIComplete != h.frontendURL+"/device?user_code="+url.QueryEscape(flow.userCode) {
		t.Errorf("verification URIs = %q, %q", flow.verificationURI, flow.verificationURIComplete)
	}
	if flow.interval < 5 || flow.expiresIn <= 0 || flow.expiresIn > int(h.ttl/time.Second) {
		t.Errorf("interval = %d, expires_in = %d", flow.interval, flow.expiresIn)
	}
	if rows := h.deviceCodeRows(t, client.id); rows != 2 {
		t.Errorf("device code rows for the client = %d, want 2 (one per device/auth)", rows)
	}

	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "authorization_pending")

	lookup := b.lookup(b.alice, flow.userCode)
	expectStatus(t, lookup, http.StatusOK)
	card := lookup.updatedData(t)
	if !strings.HasPrefix(stringField(card, "flowHandle"), "dfh_") || card["deviceLabel"] != label || card["userCode"] != flow.userCode {
		t.Errorf("review card = %v", card)
	}
	if cardClient, _ := card["client"].(map[string]any); cardClient["clientId"] != client.id || cardClient["clientType"] != "public" || cardClient["firstParty"] != false {
		t.Errorf("review card client = %v", card["client"])
	}
	if account, _ := card["account"].(map[string]any); account["userId"] != b.alice.id {
		t.Errorf("review card account = %v", card["account"])
	}
	approval := b.approveHandle(b.alice, stringField(card, "flowHandle"), flow.userCode)
	expectStatus(t, approval, http.StatusOK)
	approved := approval.updatedData(t)
	consentRequestID := stringField(approved, "consentRequestId")
	if approved["status"] != "approved" || consentRequestID == "" || approved["accountName"] != b.alice.name {
		t.Fatalf("approve answer = %v", approved)
	}

	tokens := b.redeem(flow)
	accessToken, refreshToken := stringField(tokens, "access_token"), stringField(tokens, "refresh_token")
	if !strings.HasPrefix(accessToken, "ory_at_") || !strings.HasPrefix(refreshToken, "ory_rt_") || stringField(tokens, "id_token") == "" {
		t.Fatalf("token answer lacks ory_at_/ory_rt_/id_token: %v", sortedKeys(tokens))
	}
	if rows := h.deviceCodeRows(t, client.id); rows != 1 {
		t.Errorf("§10.5.1: device code rows after issuance = %d, want 1 (Hydra deletes only the redeemed row)", rows)
	}

	// Device tokens are ordinary Hydra tokens; ext tells them apart.
	introspection := h.introspect(t, accessToken)
	ext, _ := introspection["ext"].(map[string]any)
	if introspection["active"] != true || introspection["sub"] != b.alice.identityID || introspection["client_id"] != client.id ||
		ext["flow"] != "device" || ext["uid"] != b.alice.id || ext["device_label"] != label || stringField(ext, "device_flow_id") == "" {
		t.Errorf("Hydra introspection = %v", introspection)
	}
	if _, ok := introspection["grant_type"]; ok {
		t.Logf("§10.5.1: Hydra introspection now carries a grant type: %v", introspection["grant_type"])
	}

	profile := b.bearerGet(accessToken, "/api/oauth2/user/profile")
	expectStatus(t, profile, http.StatusOK)
	if profile.updatedData(t)["name"] != b.alice.name {
		t.Errorf("profile = %s", profile.body)
	}

	session := h.consentSession(t, b.alice.identityID, consentRequestID)
	if session == nil {
		t.Fatalf("no consent session %s for the approver", consentRequestID)
	}
	consentContext, _ := session["context"].(map[string]any)
	haruki, _ := consentContext["haruki"].(map[string]any)
	if haruki["flow"] != "device" || haruki["label"] != label || haruki["label_source"] != "device" {
		t.Errorf("consent context = %v", consentContext)
	}
	consentRequest, _ := session["consent_request"].(map[string]any)
	if requestURL := stringField(consentRequest, "request_url"); !strings.Contains(requestURL, "/oauth2/device/verify") || strings.Contains(requestURL, "user_code") {
		t.Errorf("§10.5.1: consent request_url = %q, want the verify URL without user_code", requestURL)
	}
	authorizations := b.userRequest(b.alice, http.MethodGet, "/api/user/"+b.alice.id+"/oauth2/authorizations")
	expectStatus(t, authorizations, http.StatusOK)
	if entry := authorizationEntry(t, authorizations, consentRequestID); entry == nil || entry["flowType"] != "device" || entry["deviceLabel"] != label {
		t.Errorf("authorizations = %s", authorizations.body)
	}

	// The refresh token rotates through the compatibility layer verbatim.
	refreshed := b.refresh(client, refreshToken)
	expectStatus(t, refreshed, http.StatusOK)
	if !strings.HasPrefix(stringField(refreshed.json(t), "refresh_token"), "ory_rt_") {
		t.Errorf("refresh answer = %s", refreshed.body)
	}
}

func liveConfidentialBasicOnlyClientID(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{confidential: true, scopes: []string{"openid", "offline_access", "user:read"}})

	// §10.5.1: with HTTP Basic, Hydra still wants client_id in the form.
	basicOnly := h.postForm(t, h.publicURL+"/oauth2/device/auth", url.Values{"scope": {"user:read"}}, client.basic())
	if basicOnly.status == http.StatusOK || !strings.Contains(strings.ToLower(string(basicOnly.body)), "mismatch") {
		t.Errorf("§10.5.1: Hydra device/auth with Basic only answered %d %s, want the client_id mismatch error", basicOnly.status, basicOnly.body)
	}
	withForm := h.postForm(t, h.publicURL+"/oauth2/device/auth", url.Values{"scope": {"user:read"}, "client_id": {client.id}}, client.basic())
	expectStatus(t, withForm, http.StatusOK)

	// The backend injects client_id from Basic.
	flow, resp := b.deviceAuth(client, "openid offline_access user:read", "")
	expectStatus(t, resp, http.StatusOK)

	wrong := client
	wrong.secret = "not-the-secret"
	_, wrongAuth := b.deviceAuth(wrong, "user:read", "")
	expectOAuthError(t, wrongAuth, http.StatusUnauthorized, "invalid_client")

	lookup := b.lookup(b.alice, flow.userCode)
	expectStatus(t, lookup, http.StatusOK)
	card := lookup.updatedData(t)
	if cardClient, _ := card["client"].(map[string]any); cardClient["clientType"] != "confidential" || cardClient["initiatorVerified"] != true {
		t.Errorf("review card client = %v", card["client"])
	}
	expectStatus(t, b.approveHandle(b.alice, stringField(card, "flowHandle"), flow.userCode), http.StatusOK)

	// §10.5.1: Hydra authenticates the client before looking at the code: an
	// approved code with a wrong secret is 401, and the backend relays it.
	wrongFlow := *flow
	wrongFlow.client = wrong
	expectOAuthError(t, b.poll(&wrongFlow), http.StatusUnauthorized, "invalid_client")
	flow.lastPoll = wrongFlow.lastPoll

	tokens := b.redeem(flow)
	if introspection := h.introspect(t, stringField(tokens, "access_token")); introspection["client_id"] != client.id || introspection["sub"] != b.alice.identityID {
		t.Errorf("introspection = %v", introspection)
	}
}

func liveTwoIdentitiesSingleWinner(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})

	// §10.5.1: Hydra's device accept is not single use. One user code is
	// accepted on two challenges.
	raw := h.deviceAuthDirect(t, client, "user:read")
	userCode := stringField(raw, "user_code")
	for attempt := range 2 {
		browser := newHydraBrowser(t, h)
		challenge := browser.startDeviceChallenge()
		// §10.5.1: non-dev cookies are Secure, and the device CSRF cookie has no
		// client suffix.
		if secure, ok := browser.secure["ory_hydra_device_csrf"]; !ok || !secure {
			t.Errorf("§10.5.1: verify set no Secure ory_hydra_device_csrf cookie (set: %v)", browser.secure)
		}
		status, redirectTo := h.acceptDevice(t, challenge, userCode)
		if status != http.StatusOK {
			t.Errorf("§10.5.1: device accept #%d of one user code = %d, want 200 (Hydra does not enforce single use)", attempt+1, status)
			continue
		}
		// §10.5.1: without user_code on the first verify, redirect_to carries
		// only client_id and device_verifier.
		if parsed, err := url.Parse(redirectTo); err != nil || !slices.Equal(slices.Sorted(maps.Keys(parsed.Query())), []string{"client_id", "device_verifier"}) || parsed.Query().Get("client_id") != client.id {
			t.Errorf("§10.5.1: device accept redirect_to query = %v", parsed.Query())
		}
	}

	// The backend lets exactly one account claim the code.
	flow, _ := b.deviceAuth(client, "user:read", "")
	var wg sync.WaitGroup
	results := make([]liveResponse, 2)
	start := make(chan struct{})
	for index, user := range []liveUser{b.alice, b.bob} {
		wg.Go(func() {
			<-start
			results[index] = b.lookup(user, flow.userCode)
		})
	}
	close(start)
	wg.Wait()
	winner, loser := b.alice, b.bob
	winnerLookup, loserLookup := results[0], results[1]
	if results[1].status == http.StatusOK {
		winner, loser = b.bob, b.alice
		winnerLookup, loserLookup = results[1], results[0]
	}
	expectStatus(t, winnerLookup, http.StatusOK)
	expectBrowserCode(t, loserLookup, http.StatusBadRequest, "invalid_code")
	expectBrowserCode(t, b.lookup(loser, flow.userCode), http.StatusBadRequest, "invalid_code")
	// The loser cannot approve with the winner's handle either.
	handle := stringField(winnerLookup.updatedData(t), "flowHandle")
	if resp := b.approveHandle(loser, handle, flow.userCode); resp.status == http.StatusOK {
		t.Fatalf("the losing account approved with the winner's handle: %s", resp.body)
	}
	expectStatus(t, b.approveHandle(winner, handle, flow.userCode), http.StatusOK)

	tokens := b.redeem(flow)
	if introspection := h.introspect(t, stringField(tokens, "access_token")); introspection["sub"] != winner.identityID {
		t.Errorf("token subject = %v, want the winner %s", introspection["sub"], winner.identityID)
	}
	if sessions := h.consentSessions(t, loser.identityID); len(sessions) != 0 {
		t.Errorf("the losing account has %d consent sessions", len(sessions))
	}
}

func liveDenyAccessDenied(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})

	// §10.5.1: a login reject never reaches the device. The browser gets bare
	// JSON on the Hydra host, the device stays pending, and the same user
	// code can still be accepted.
	raw := h.deviceAuthDirect(t, client, "user:read")
	browser := newHydraBrowser(t, h)
	challenge := browser.startDeviceChallenge()
	status, redirectTo := h.acceptDevice(t, challenge, stringField(raw, "user_code"))
	if status != http.StatusOK {
		t.Fatalf("device accept = %d", status)
	}
	loginChallenge := browser.followToFrontend(redirectTo, "/oauth2/login", "login_challenge")
	rejected := h.adminJSON(t, http.MethodPut, "/admin/oauth2/auth/requests/login/reject?login_challenge="+url.QueryEscape(loginChallenge), map[string]any{"error": "access_denied"})
	expectStatus(t, rejected, http.StatusOK)
	final := browser.get(stringField(rejected.json(t), "redirect_to"))
	if final.status == http.StatusFound || final.status < 400 {
		t.Errorf("§10.5.1: after a login reject Hydra answered %d (Location %q), want a bare error on the Hydra host", final.status, final.header.Get("Location"))
	}
	expectOAuthError(t, h.tokenDirect(t, client, stringField(raw, "device_code")), http.StatusBadRequest, "authorization_pending")
	if status, _ := h.acceptDevice(t, newHydraBrowser(t, h).startDeviceChallenge(), stringField(raw, "user_code")); status != http.StatusOK {
		t.Errorf("§10.5.1: the user code was not acceptable again after a reject (%d)", status)
	}

	// The backend records the refusal and the device sees access_denied.
	flow, _ := b.deviceAuth(client, "user:read", "")
	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "authorization_pending")
	lookup := b.lookup(b.alice, flow.userCode)
	expectStatus(t, lookup, http.StatusOK)
	deny := b.browserPost(b.alice, "/api/oauth2/device/deny", map[string]any{"flowHandle": stringField(lookup.updatedData(t), "flowHandle"), "reason": "not_initiated_by_me"})
	expectStatus(t, deny, http.StatusOK)
	if deny.updatedData(t)["status"] != "denied" {
		t.Errorf("deny answer = %s", deny.body)
	}
	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "access_denied")
	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "access_denied")
	if !strings.Contains(b.logs.String(), "event=phishing_signal") {
		t.Error("not_initiated_by_me logged no phishing_signal")
	}
	if sessions := h.consentSessions(t, b.alice.identityID); len(sessions) != 0 {
		t.Errorf("a denied flow left %d consent sessions", len(sessions))
	}
}

func liveExpiryExpiredToken(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})

	raw := h.deviceAuthDirect(t, client, "user:read")
	unclaimed, _ := b.deviceAuth(client, "user:read", "")
	claimed, _ := b.deviceAuth(client, "user:read", "")
	lookup := b.lookup(b.alice, claimed.userCode)
	expectStatus(t, lookup, http.StatusOK)
	handle := stringField(lookup.updatedData(t), "flowHandle")
	expectOAuthError(t, b.poll(unclaimed), http.StatusBadRequest, "authorization_pending")

	waitUntil(t, unclaimed.expiresAt().Add(time.Second))

	// §10.5.1: Hydra keeps answering authorization_pending for a code that was
	// never approved, even past its expiry.
	expectOAuthError(t, h.tokenDirect(t, client, stringField(raw, "device_code")), http.StatusBadRequest, "authorization_pending")
	// The backend turns it into expired_token.
	expectOAuthError(t, b.poll(unclaimed), http.StatusBadRequest, "expired_token")
	expectOAuthError(t, b.poll(unclaimed), http.StatusBadRequest, "expired_token")
	expectOAuthError(t, b.poll(claimed), http.StatusBadRequest, "expired_token")
	expectBrowserCode(t, b.lookup(b.bob, unclaimed.userCode), http.StatusBadRequest, "invalid_code")
	expectBrowserCode(t, b.approveHandle(b.alice, handle, claimed.userCode), http.StatusGone, "code_expired")

	// §10.5.1: an expired user code is refused by device accept (400).
	if status, _ := h.acceptDevice(t, newHydraBrowser(t, h).startDeviceChallenge(), stringField(raw, "user_code")); status != http.StatusBadRequest {
		t.Errorf("§10.5.1: device accept of an expired user code = %d, want 400", status)
	}
}

func liveSlowDown(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})

	// §10.5.1: Hydra never answers slow_down.
	raw := h.deviceAuthDirect(t, client, "user:read")
	for range 3 {
		expectOAuthError(t, h.tokenDirect(t, client, stringField(raw, "device_code")), http.StatusBadRequest, "authorization_pending")
	}

	flow, _ := b.deviceAuth(client, "user:read", "")
	expectOAuthError(t, b.pollNow(flow), http.StatusBadRequest, "authorization_pending")
	early := b.pollNow(flow)
	expectOAuthError(t, early, http.StatusBadRequest, "slow_down")
	assertNoStore(t, early)
	if interval := intField(early.json(t), "interval"); interval != 10 {
		t.Fatalf("slow_down interval = %d, want 10", interval)
	}
	// The new interval sticks: the old 5 s is now early too.
	time.Sleep(5500 * time.Millisecond)
	again := b.pollNow(flow)
	expectOAuthError(t, again, http.StatusBadRequest, "slow_down")
	if interval := intField(again.json(t), "interval"); interval != 15 {
		t.Fatalf("second slow_down interval = %d, want 15", interval)
	}
	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "authorization_pending")
	if !strings.Contains(b.logs.String(), "event=poll_slow_down") {
		t.Error("no poll_slow_down log line")
	}
}

func liveHydraDirectTokenRejectsWrappedCode(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	public := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})
	confidential := b.createClient(liveClientSpec{confidential: true, scopes: []string{"offline_access", "user:read"}})

	for _, client := range []liveClient{public, confidential} {
		flow, _ := b.deviceAuth(client, "user:read", "")
		// §10.5.1: Hydra cannot redeem hdc_: 400 invalid_grant, not 5xx.
		expectOAuthError(t, h.tokenDirect(t, client, flow.deviceCode), http.StatusBadRequest, "invalid_grant")
		// The compatibility layer refuses a wrapped code presented by another client.
		other := public
		if client.id == public.id {
			other = confidential
		}
		expectOAuthError(t, b.pollAs(flow, other), http.StatusBadRequest, "invalid_grant")
	}
	unknown := &liveFlow{client: public, deviceCode: "hdc_" + base64.RawURLEncoding.EncodeToString(randomBytes(32)), interval: 5}
	expectOAuthError(t, b.pollNow(unknown), http.StatusBadRequest, "invalid_grant")

	// §10.5.1: Hydra authenticates the client first: a wrong secret is 401 even
	// for a valid ory_dc_ code.
	raw := h.deviceAuthDirect(t, confidential, "user:read")
	wrong := confidential
	wrong.secret = "not-the-secret"
	expectOAuthError(t, h.tokenDirect(t, wrong, stringField(raw, "device_code")), http.StatusUnauthorized, "invalid_client")
}

func livePerDeviceRevokeKillsRefreshedATandRT(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})

	first, _ := b.deviceAuth(client, "offline_access user:read", "device one")
	firstConsent := b.approve(b.alice, first)
	firstTokens := b.redeem(first)
	second, _ := b.deviceAuth(client, "offline_access user:read", "device two")
	b.approve(b.alice, second)
	secondTokens := b.redeem(second)

	refreshed := b.refresh(client, stringField(firstTokens, "refresh_token"))
	expectStatus(t, refreshed, http.StatusOK)
	accessToken2, refreshToken2 := stringField(refreshed.json(t), "access_token"), stringField(refreshed.json(t), "refresh_token")

	authorizations := b.userRequest(b.alice, http.MethodGet, "/api/user/"+b.alice.id+"/oauth2/authorizations")
	if entry := authorizationEntry(t, authorizations, firstConsent); entry == nil || entry["deviceLabel"] != "device one" {
		t.Fatalf("authorizations = %s", authorizations.body)
	}
	revokePath := "/api/user/%s/oauth2/authorizations/" + url.PathEscape(client.id) + "/consents/" + url.PathEscape(firstConsent)
	// Another account cannot revoke it: 404 and nothing is revoked.
	expectBrowserCode(t, b.userRequest(b.bob, http.MethodDelete, fmt.Sprintf(revokePath, b.bob.id)), http.StatusNotFound, "authorization_not_found")
	if h.introspect(t, accessToken2)["active"] != true {
		t.Fatal("another account's revoke attempt revoked the token")
	}

	revoked := b.userRequest(b.alice, http.MethodDelete, fmt.Sprintf(revokePath, b.alice.id))
	expectStatus(t, revoked, http.StatusOK)
	if revoked.updatedData(t)["revoked"] != true {
		t.Errorf("revoke answer = %s", revoked.body)
	}
	// §10.5.1: revoking by consent_request_id kills the chain's AT and RT,
	// refreshed ones included.
	for name, token := range map[string]string{"first access token": stringField(firstTokens, "access_token"), "refreshed access token": accessToken2} {
		if h.introspect(t, token)["active"] != false {
			t.Errorf("%s is still active after the per-device revoke", name)
		}
	}
	expectOAuthError(t, b.refresh(client, refreshToken2), http.StatusBadRequest, "invalid_grant")
	if h.consentSession(t, b.alice.identityID, firstConsent) != nil {
		t.Error("the revoked consent session is still listed")
	}
	// The other device keeps working.
	if h.introspect(t, stringField(secondTokens, "access_token"))["active"] != true {
		t.Error("revoking one device revoked the other")
	}
	expectStatus(t, b.refresh(client, stringField(secondTokens, "refresh_token")), http.StatusOK)
}

func liveDisabledClientBlocked(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})

	redeemed, _ := b.deviceAuth(client, "offline_access user:read", "")
	b.approve(b.alice, redeemed)
	tokens := b.redeem(redeemed)
	approved, _ := b.deviceAuth(client, "offline_access user:read", "")
	b.approve(b.alice, approved)
	claimed, _ := b.deviceAuth(client, "offline_access user:read", "")
	lookup := b.lookup(b.alice, claimed.userCode)
	expectStatus(t, lookup, http.StatusOK)

	disable := b.adminRequest(http.MethodPut, "/api/admin/oauth-clients/"+url.PathEscape(client.id)+"/active", map[string]any{"active": false})
	expectStatus(t, disable, http.StatusOK)
	disabledAt := time.Now()
	result := disable.updatedData(t)
	if result["active"] != false || result["revocationComplete"] != true || intField(result, "revokedSubjects") < 1 {
		t.Fatalf("disable answer = %v", result)
	}

	// Disabling really revokes (ory-suite-usage §10.4): the access token is
	// inactive and the refresh token no longer rotates.
	if h.introspect(t, stringField(tokens, "access_token"))["active"] != false {
		t.Error("the access token survived disabling the client")
	}
	expectOAuthError(t, b.refresh(client, stringField(tokens, "refresh_token")), http.StatusBadRequest, "invalid_grant")
	// §10.5.1: Hydra does not look at metadata.haruki.active.
	if raw := h.postForm(t, h.publicURL+"/oauth2/device/auth", url.Values{"client_id": {client.id}, "scope": {"user:read"}}, ""); raw.status != http.StatusOK {
		t.Errorf("§10.5.1: Hydra refused device/auth for a disabled client (%d %s); it used to ignore haruki.active", raw.status, raw.body)
	}

	waitUntil(t, disabledAt.Add(liveClientCacheTTL))
	_, refused := b.deviceAuth(client, "user:read", "")
	expectOAuthError(t, refused, http.StatusBadRequest, "unauthorized_client")
	expectOAuthError(t, b.poll(approved), http.StatusBadRequest, "access_denied")
	expectBrowserCode(t, b.approveHandle(b.alice, stringField(lookup.updatedData(t), "flowHandle"), claimed.userCode), http.StatusForbidden, "client_unavailable")
	if introspected := b.internalIntrospect(stringField(tokens, "access_token")); introspected.json(t)["active"] != false {
		t.Errorf("internal introspection = %s", introspected.body)
	}
	if sessions := h.consentSessions(t, b.alice.identityID); len(sessions) != 0 {
		t.Errorf("%d consent sessions survived disabling the client", len(sessions))
	}
}

func liveDiscoveryAdvertisesBackendEndpoints(t *testing.T, h *liveHydra) {
	for _, document := range []string{"/.well-known/openid-configuration", "/.well-known/oauth-authorization-server"} {
		resp := h.get(t, h.publicURL+document)
		expectStatus(t, resp, http.StatusOK)
		discovery := resp.json(t)
		if discovery["device_authorization_endpoint"] != h.issuerURL+"/api/oauth2/device/auth" || discovery["token_endpoint"] != h.issuerURL+"/api/oauth2/token" {
			t.Errorf("%s: device_authorization_endpoint = %v, token_endpoint = %v", document, discovery["device_authorization_endpoint"], discovery["token_endpoint"])
		}
		// §10.5.1: the WEBFINGER overrides move only those two endpoints.
		if discovery["issuer"] != h.issuerURL && discovery["issuer"] != h.issuerURL+"/" {
			t.Errorf("%s: issuer = %v", document, discovery["issuer"])
		}
		if discovery["authorization_endpoint"] != h.issuerURL+"/oauth2/auth" {
			t.Errorf("%s: authorization_endpoint = %v", document, discovery["authorization_endpoint"])
		}
		if grants, _ := discovery["grant_types_supported"].([]any); !slices.Contains(grants, any(deviceGrantType)) {
			t.Errorf("%s: grant_types_supported = %v", document, discovery["grant_types_supported"])
		}
	}
	// ...and not Hydra's verification_uri, which the backend rewrites.
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"user:read"}})
	if raw := h.deviceAuthDirect(t, client, "user:read"); raw["verification_uri"] != h.issuerURL+"/oauth2/device/verify" {
		t.Errorf("§10.5.1: Hydra verification_uri = %v", raw["verification_uri"])
	}
	if flow, _ := b.deviceAuth(client, "user:read", ""); flow.verificationURI != h.frontendURL+"/device" {
		t.Errorf("backend verification_uri = %q", flow.verificationURI)
	}
}

func liveReaperRevokesUnredeemed(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})
	flow, _ := b.deviceAuth(client, "offline_access user:read", "")
	consentRequestID := b.approve(b.alice, flow)
	if h.consentSession(t, b.alice.identityID, consentRequestID) == nil {
		t.Fatal("the approval left no consent session")
	}
	if b.unredeemedCount() != 1 {
		t.Fatalf("unredeemed flows = %d, want 1", b.unredeemedCount())
	}

	// Never polled: the reaper revokes the consent session once the flow is
	// liveReaperGrace past its expiry.
	waitFor(t, flow.expiresAt().Add(liveReaperGrace+15*time.Second), "the reaper to revoke the consent session", func() bool {
		return h.consentSession(t, b.alice.identityID, consentRequestID) == nil
	})
	if b.unredeemedCount() != 0 || !strings.Contains(b.logs.String(), "event=reaped") {
		t.Errorf("unredeemed flows = %d; reaped logged: %v", b.unredeemedCount(), strings.Contains(b.logs.String(), "event=reaped"))
	}
	// §10.5.1: the revocation cascaded to the device code row, so the late
	// poll is Hydra's invalid_grant, which the backend reports as expired.
	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "expired_token")
}

func liveApprovedThenExpiredIsReaped(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})
	flow, _ := b.deviceAuth(client, "offline_access user:read", "")
	consentRequestID := b.approve(b.alice, flow)

	// Redeemed too late: Hydra refuses the expired code, the backend answers
	// expired_token and keeps the flow for the reaper.
	waitUntil(t, flow.expiresAt().Add(2*time.Second))
	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "expired_token")
	if b.unredeemedCount() != 1 {
		t.Fatalf("unredeemed flows after the late poll = %d, want 1 (left for the reaper)", b.unredeemedCount())
	}
	// §10.5.1: Hydra refuses the redemption but keeps the consent session.
	if h.consentSession(t, b.alice.identityID, consentRequestID) == nil {
		t.Fatal("§10.5.1: Hydra dropped the consent session of an expired approved code by itself")
	}
	waitFor(t, flow.expiresAt().Add(liveReaperGrace+15*time.Second), "the reaper to revoke the consent session", func() bool {
		return h.consentSession(t, b.alice.identityID, consentRequestID) == nil
	})
	if b.unredeemedCount() != 0 {
		t.Errorf("unredeemed flows after reaping = %d", b.unredeemedCount())
	}
	expectOAuthError(t, b.poll(flow), http.StatusBadRequest, "expired_token")
}

func liveJanitorSQLDeletesOnlyExpired(t *testing.T, h *liveHydra) {
	janitorSQL, grace, batch := janitorFromCompose(t)
	if grace != "1 hour" || batch != "5000" {
		t.Errorf("janitor defaults = grace %q, batch %q; want 1 hour, 5000", grace, batch)
	}
	conn, err := pgx.Connect(t.Context(), h.dsn)
	if err != nil {
		t.Fatalf("connect to Hydra's Postgres: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	// The janitor compares with the UTC wall clock, so the session time zone
	// must not matter.
	if _, err := conn.Exec(t.Context(), "SET TIME ZONE 'Asia/Shanghai'"); err != nil {
		t.Fatal(err)
	}

	// §10.5.1: expires_at is a timestamp without time zone, written in UTC.
	var dataType string
	if err := conn.QueryRow(t.Context(), `SELECT data_type FROM information_schema.columns WHERE table_name = 'hydra_oauth2_device_auth_codes' AND column_name = 'expires_at'`).Scan(&dataType); err != nil || dataType != "timestamp without time zone" {
		t.Fatalf("§10.5.1: expires_at type = %q (%v)", dataType, err)
	}
	var indexes []string
	rows, err := conn.Query(t.Context(), `SELECT indexdef FROM pg_indexes WHERE tablename = 'hydra_oauth2_device_auth_codes'`)
	if err == nil {
		indexes, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(indexes, func(def string) bool { return strings.Contains(def, "expires_at") }) {
		t.Logf("§10.5.1 changed: expires_at is now indexed: %v", indexes)
	}

	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"user:read"}})
	for range 6 {
		h.deviceAuthDirect(t, client, "user:read")
	}
	signatures := janitorRows(t, conn, client.id)
	if len(signatures) != 6 {
		t.Fatalf("device code rows = %d, want 6", len(signatures))
	}
	var skew float64
	if err := conn.QueryRow(t.Context(), `SELECT abs(extract(epoch FROM expires_at - ((now() AT TIME ZONE 'UTC') + $2::interval))) FROM hydra_oauth2_device_auth_codes WHERE device_code_signature = $1`, signatures[5], fmt.Sprintf("%d seconds", int(h.ttl.Seconds()))).Scan(&skew); err != nil || skew > 30 {
		t.Fatalf("§10.5.1: expires_at is %v s off the UTC wall clock + TTL (%v)", skew, err)
	}
	backdate := func(signature, age string) {
		t.Helper()
		var err error
		if age == "" {
			_, err = conn.Exec(t.Context(), `UPDATE hydra_oauth2_device_auth_codes SET expires_at = NULL WHERE device_code_signature = $1`, signature)
		} else {
			_, err = conn.Exec(t.Context(), `UPDATE hydra_oauth2_device_auth_codes SET expires_at = (now() AT TIME ZONE 'UTC') - $2::interval WHERE device_code_signature = $1`, signature, age)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	doomed := signatures[:3]
	for _, signature := range doomed {
		backdate(signature, "2 hours")
	}
	backdate(signatures[3], "30 minutes") // expired, but within the grace period
	backdate(signatures[4], "")           // NULL expires_at is never deleted
	// signatures[5] has not expired.

	// The loop of the janitor's shell, with a batch of 2 to exercise it.
	const testBatch = 2
	statement := strings.NewReplacer(":'grace'", quotePostgresLiteral(grace), ":batch", strconv.Itoa(testBatch)).Replace(janitorSQL)
	var batches []int
	for {
		var deleted int
		if err := conn.QueryRow(t.Context(), statement).Scan(&deleted); err != nil {
			t.Fatalf("janitor SQL: %v\n%s", err, statement)
		}
		batches = append(batches, deleted)
		if deleted < testBatch {
			break
		}
	}
	if !slices.Equal(batches, []int{2, 1}) {
		t.Errorf("janitor batches deleted %v, want [2 1] (only the three rows past the grace period)", batches)
	}
	if remaining := janitorRows(t, conn, client.id); !slices.Equal(remaining, signatures[3:]) {
		t.Errorf("rows left = %d, want the 3 rows within grace, without expiry, and not expired", len(remaining))
	}
}

func liveGoXOAuth2Sample(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	client := b.createClient(liveClientSpec{scopes: []string{"offline_access", "user:read"}})
	// The compatibility layer's next answer is a 503, as when the runtime
	// switch cannot be read; the sample must back off and carry on.
	b.failNextTokenRequests(1)

	conf := &oauth2.Config{
		ClientID: client.id,
		Scopes:   []string{"offline_access", "user:read"},
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: b.baseURL + "/api/oauth2/device/auth",
			TokenURL:      b.baseURL + "/api/oauth2/token",
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*h.ttl)
	defer cancel()
	approved := make(chan error, 1)
	var shownCode string
	tokens, err := deviceLogin(ctx, conf, "Go sample @ live-host", func(da *oauth2.DeviceAuthResponse) {
		// The user approves in the browser while the program polls.
		shownCode = da.UserCode
		b.recordSecrets(da.DeviceCode)
		go func() { approved <- b.approveUserCode(b.alice, da.UserCode) }()
	})
	if err != nil {
		t.Fatalf("deviceLogin: %v", err)
	}
	if err := <-approved; err != nil {
		t.Fatal(err)
	}
	b.recordSecrets(shownCode, strings.ReplaceAll(shownCode, "-", ""))
	name, err := authorizedAccountName(ctx, conf, tokens, b.baseURL)
	if err != nil || name != b.alice.name {
		t.Fatalf("authorized as %q (%v), want %q", name, err, b.alice.name)
	}
	if tokens.RefreshToken == "" || tokens.Expiry.IsZero() {
		t.Errorf("token = %+v", tokens)
	}

	polls := b.tokenRequestTimes()
	if b.injectedFailures() != 1 || len(polls) < 2 {
		t.Fatalf("injected 503s = %d, token requests = %d", b.injectedFailures(), len(polls))
	}
	if gap := polls[1].Sub(polls[0]); gap < 9500*time.Millisecond {
		t.Errorf("the poll after the 503 came %s later, want the doubled interval (10 s)", gap)
	}
	if strings.Contains(b.logs.String(), "event=poll_slow_down") {
		t.Error("the sample polled too early (slow_down)")
	}
}

func liveStationScopeInternalIntrospect(t *testing.T, h *liveHydra) {
	b := newLiveBackend(t, h)
	scopes := []string{"user:read", "offline_access", "station:room:write"}
	client := b.createClient(liveClientSpec{scopes: scopes})

	label := "Haruki-Client @ station-host"
	flow, _ := b.deviceAuth(client, strings.Join(scopes, " "), label)
	lookup := b.lookup(b.alice, flow.userCode)
	expectStatus(t, lookup, http.StatusOK)
	risks := map[string]string{}
	cardScopes, _ := lookup.updatedData(t)["scopes"].([]any)
	for _, entry := range cardScopes {
		scope, _ := entry.(map[string]any)
		risks[stringField(scope, "scope")] = stringField(scope, "risk")
	}
	if risks["station:room:write"] != "write" {
		t.Errorf("review card risks = %v, want station:room:write as write", risks)
	}
	approval := b.approveHandle(b.alice, stringField(lookup.updatedData(t), "flowHandle"), flow.userCode)
	expectStatus(t, approval, http.StatusOK)
	consentRequestID := stringField(approval.updatedData(t), "consentRequestId")
	accessToken := stringField(b.redeem(flow), "access_token")

	missing := b.request(http.MethodPost, b.baseURL+oauth2Module.InternalIntrospectPath, "application/x-www-form-urlencoded", "token="+url.QueryEscape(accessToken), nil)
	if missing.status != http.StatusUnauthorized {
		t.Errorf("internal introspection without the internal token = %d", missing.status)
	}
	introspected := b.internalIntrospect(accessToken)
	expectStatus(t, introspected, http.StatusOK)
	assertNoStore(t, introspected)
	result := introspected.json(t)
	gotScopes := strings.Fields(stringField(result, "scope"))
	slices.Sort(gotScopes)
	wantScopes := slices.Sorted(slices.Values(scopes))
	if result["active"] != true || result["user_id"] != b.alice.id || result["client_id"] != client.id || !slices.Equal(gotScopes, wantScopes) ||
		result["device_label"] != label || intField(result, "exp") <= intField(result, "iat") {
		t.Errorf("internal introspection = %v", result)
	}
	for _, leaked := range []string{"name", "email", "sub", "ext"} {
		if _, ok := result[leaked]; ok {
			t.Errorf("internal introspection leaks %q", leaked)
		}
	}

	revoked := b.userRequest(b.alice, http.MethodDelete, "/api/user/"+b.alice.id+"/oauth2/authorizations/"+url.PathEscape(client.id)+"/consents/"+url.PathEscape(consentRequestID))
	expectStatus(t, revoked, http.StatusOK)
	after := b.internalIntrospect(accessToken)
	expectStatus(t, after, http.StatusOK)
	if keys := sortedKeys(after.json(t)); !slices.Equal(keys, []string{"active"}) || after.json(t)["active"] != false {
		t.Errorf("internal introspection after the per-device revoke = %s", after.body)
	}
}

// --- The Go sample (oauth2-integration §4A.8) ---------------------------

// deviceLogin signs a headless program in with the OAuth2 device
// authorization grant (RFC 8628) through golang.org/x/oauth2. It is the Go
// example of the integration docs.
//
// conf names the Toolbox endpoints, https://toolbox-api-direct.haruki.seiunx.com
// + /api/oauth2/device/auth and /api/oauth2/token (both are also in the
// discovery document). For a public client leave ClientSecret empty and set
// AuthStyle to oauth2.AuthStyleInParams, so client_id goes in the form and no
// Basic header is sent; a confidential client must add its Basic credentials
// itself (DeviceAuth never sends the secret), for example with an
// *http.Client in ctx (oauth2.HTTPClient) whose transport sets them.
//
// show must print the user code and the complete verification URI on the
// local console only, never through a chat or any other channel, with the
// expiry and a warning to approve only a request the user just started.
//
// DeviceAccessToken keeps polling only on authorization_pending and slow_down;
// it returns on anything else. A 429 (wait Retry-After), a 5xx such as 503
// temporarily_unavailable, and a network error are not final: poll again with
// the same da until da.Expiry, doubling the interval after a 5xx or a network
// error. access_denied, expired_token, invalid_grant and invalid_client are
// final.
func deviceLogin(ctx context.Context, conf *oauth2.Config, label string, show func(*oauth2.DeviceAuthResponse)) (*oauth2.Token, error) {
	da, err := conf.DeviceAuth(ctx, oauth2.SetAuthURLParam("device_label", label))
	if err != nil {
		return nil, fmt.Errorf("start device authorization: %w", err)
	}
	show(da)

	interval := max(da.Interval, 5)
	for {
		// DeviceAccessToken waits da.Interval before every poll.
		da.Interval = interval
		token, err := conf.DeviceAccessToken(ctx, da)
		if err == nil {
			return token, nil
		}
		if ctx.Err() != nil || !time.Now().Before(da.Expiry) {
			return nil, fmt.Errorf("device authorization expired: %w", err)
		}
		var retrieveErr *oauth2.RetrieveError
		if !errors.As(err, &retrieveErr) {
			interval = min(interval*2, 60) // network error
			continue
		}
		switch status := retrieveErr.Response.StatusCode; {
		case status == http.StatusTooManyRequests:
			if err := sleepContext(ctx, retryAfter(retrieveErr.Response, interval)); err != nil {
				return nil, err
			}
		case status >= http.StatusInternalServerError:
			interval = min(interval*2, 60)
		default:
			return nil, err // access_denied, expired_token, invalid_grant, invalid_client
		}
	}
}

// authorizedAccountName returns the Toolbox account the token acts for; the
// program must show it ("Authorized as Toolbox account <name>").
func authorizedAccountName(ctx context.Context, conf *oauth2.Config, token *oauth2.Token, baseURL string) (string, error) {
	resp, err := conf.Client(ctx, token).Get(baseURL + "/api/oauth2/user/profile")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("profile: HTTP %d", resp.StatusCode)
	}
	var profile struct {
		UpdatedData struct {
			Name string `json:"name"`
		} `json:"updatedData"`
	}
	if err := json.UnmarshalRead(resp.Body, &profile); err != nil {
		return "", err
	}
	return profile.UpdatedData.Name, nil
}

// retryAfter reads Retry-After in seconds, falling back to the poll interval.
func retryAfter(resp *http.Response, fallbackSeconds int64) time.Duration {
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(fallbackSeconds) * time.Second
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// --- Hydra --------------------------------------------------------------

type liveHydra struct {
	publicURL, adminURL, issuerURL, frontendURL, dsn string
	version                                          string
	ttl                                              time.Duration
	config                                           *harukiOAuth2.HydraConfig
	http                                             *http.Client
}

func connectLiveHydra(t *testing.T) *liveHydra {
	t.Helper()
	ttl, err := time.ParseDuration(liveEnv("HYDRA_IT_USER_CODE_TTL"))
	if err != nil || ttl < time.Minute {
		// approve needs 30 s of remaining lifetime (min_remaining_seconds_to_approve).
		t.Fatalf("HYDRA_IT_USER_CODE_TTL = %q: want a duration of at least 1m", liveEnv("HYDRA_IT_USER_CODE_TTL"))
	}
	h := &liveHydra{
		publicURL:   strings.TrimRight(liveEnv("HYDRA_IT_PUBLIC_URL"), "/"),
		adminURL:    strings.TrimRight(liveEnv("HYDRA_IT_ADMIN_URL"), "/"),
		issuerURL:   strings.TrimRight(liveEnv("HYDRA_IT_ISSUER_URL"), "/"),
		frontendURL: strings.TrimRight(liveEnv("HYDRA_IT_FRONTEND_URL"), "/"),
		dsn:         liveEnv("HYDRA_IT_DSN"),
		ttl:         ttl,
		http: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		resp, err := h.http.Get(h.adminURL + "/health/ready")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Hydra admin %s is not ready (%v); start external/hydra/it/docker-compose.device-it.yml", h.adminURL, err)
		}
		time.Sleep(time.Second)
	}
	h.version = stringField(h.get(t, h.adminURL+"/version").json(t), "version")
	if want := strings.TrimSpace(os.Getenv("HYDRA_IT_VERSION")); want != "" && h.version != want {
		t.Fatalf("Hydra reports version %q, HYDRA_IT_VERSION is %q", h.version, want)
	}
	h.config = harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		PublicURL: h.publicURL, BrowserURL: h.issuerURL, AdminURL: h.adminURL, RequestTimeout: 10 * time.Second,
	})
	return h
}

func (h *liveHydra) do(t *testing.T, req *http.Request) liveResponse {
	t.Helper()
	resp, err := h.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return liveResponse{status: resp.StatusCode, header: resp.Header, body: body}
}

func (h *liveHydra) get(t *testing.T, rawURL string) liveResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h.do(t, req)
}

func (h *liveHydra) postForm(t *testing.T, rawURL string, form url.Values, authorization string) liveResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return h.do(t, req)
}

func (h *liveHydra) adminJSON(t *testing.T, method, path string, body any) liveResponse {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, h.adminURL+path, strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return h.do(t, req)
}

// deviceAuthDirect asks Hydra itself for a device code, bypassing the backend.
func (h *liveHydra) deviceAuthDirect(t *testing.T, client liveClient, scope string) map[string]any {
	t.Helper()
	resp := h.postForm(t, h.publicURL+"/oauth2/device/auth", url.Values{"client_id": {client.id}, "scope": {scope}}, client.basic())
	expectStatus(t, resp, http.StatusOK)
	return resp.json(t)
}

// tokenDirect polls Hydra's own token endpoint.
func (h *liveHydra) tokenDirect(t *testing.T, client liveClient, deviceCode string) liveResponse {
	t.Helper()
	return h.postForm(t, h.publicURL+"/oauth2/token", url.Values{"grant_type": {deviceGrantType}, "device_code": {deviceCode}, "client_id": {client.id}}, client.basic())
}

func (h *liveHydra) introspect(t *testing.T, token string) map[string]any {
	t.Helper()
	resp := h.postForm(t, h.adminURL+"/admin/oauth2/introspect", url.Values{"token": {token}}, "")
	expectStatus(t, resp, http.StatusOK)
	return resp.json(t)
}

func (h *liveHydra) consentSessions(t *testing.T, subject string) []map[string]any {
	t.Helper()
	resp := h.get(t, h.adminURL+"/admin/oauth2/auth/sessions/consent?subject="+url.QueryEscape(subject))
	expectStatus(t, resp, http.StatusOK)
	var sessions []map[string]any
	if err := json.Unmarshal(resp.body, &sessions); err != nil {
		t.Fatalf("consent sessions: %v", err)
	}
	return sessions
}

func (h *liveHydra) consentSession(t *testing.T, subject, consentRequestID string) map[string]any {
	t.Helper()
	for _, session := range h.consentSessions(t, subject) {
		if session["consent_request_id"] == consentRequestID {
			return session
		}
	}
	return nil
}

// acceptDevice is H9c: the only admin endpoint of the device leg.
func (h *liveHydra) acceptDevice(t *testing.T, challenge, userCode string) (int, string) {
	t.Helper()
	resp := h.adminJSON(t, http.MethodPut, "/admin/oauth2/auth/requests/device/accept?device_challenge="+url.QueryEscape(challenge), map[string]any{"user_code": userCode})
	if resp.status != http.StatusOK {
		return resp.status, ""
	}
	return resp.status, stringField(resp.json(t), "redirect_to")
}

func (h *liveHydra) deviceCodeRows(t *testing.T, clientID string) int {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), h.dsn)
	if err != nil {
		t.Fatalf("connect to Hydra's Postgres: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	if err := conn.QueryRow(t.Context(), `SELECT count(*) FROM hydra_oauth2_device_auth_codes WHERE client_id = $1`, clientID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// hydraBrowser drives Hydra's browser leg directly, the way the backend's
// chain does (§10.5.7): a name→value cookie jar replayed over plain http, and
// issuer verify URLs rewritten to the public endpoint. It proves the Hydra
// behaviour of §10.5.1 independently of the backend.
type hydraBrowser struct {
	t   *testing.T
	h   *liveHydra
	jar map[string]string
	// secure records the Secure attribute of every cookie Hydra set.
	secure map[string]bool
}

func newHydraBrowser(t *testing.T, h *liveHydra) *hydraBrowser {
	return &hydraBrowser{t: t, h: h, jar: map[string]string{}, secure: map[string]bool{}}
}

func (b *hydraBrowser) get(rawURL string) liveResponse {
	b.t.Helper()
	if rest, ok := strings.CutPrefix(rawURL, b.h.issuerURL+"/oauth2/device/verify"); ok {
		rawURL = b.h.publicURL + "/oauth2/device/verify" + rest
	}
	req, err := http.NewRequestWithContext(b.t.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	cookies := make([]string, 0, len(b.jar))
	for name, value := range b.jar {
		cookies = append(cookies, name+"="+value)
	}
	if len(cookies) > 0 {
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	resp := b.h.do(b.t, req)
	for _, cookie := range (&http.Response{Header: resp.header}).Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.jar, cookie.Name)
			continue
		}
		b.jar[cookie.Name] = cookie.Value
		b.secure[cookie.Name] = cookie.Secure
	}
	return resp
}

// startDeviceChallenge is H9b without a user code: Hydra redirects to the
// frontend's /device with a device challenge.
func (b *hydraBrowser) startDeviceChallenge() string {
	b.t.Helper()
	return b.followToFrontend(b.h.publicURL+"/oauth2/device/verify", "/device", "device_challenge")
}

// followToFrontend GETs rawURL and expects a 302 to the frontend path carrying
// the named challenge.
func (b *hydraBrowser) followToFrontend(rawURL, path, param string) string {
	b.t.Helper()
	resp := b.get(rawURL)
	location, err := url.Parse(resp.header.Get("Location"))
	if resp.status != http.StatusFound || err != nil || urlOrigin(location) != b.h.frontendURL || location.Path != path || location.Query().Get(param) == "" {
		b.t.Fatalf("GET %s = %d Location %q, want a 302 to %s?%s=…", strings.SplitN(rawURL, "?", 2)[0], resp.status, resp.header.Get("Location"), path, param)
	}
	return location.Query().Get(param)
}

// --- Backend ------------------------------------------------------------

type liveUser struct {
	id, name, email, identityID, session string
}

type liveClient struct {
	id, secret   string
	confidential bool
}

func (c liveClient) basic() string {
	if !c.confidential {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(c.id)+":"+url.QueryEscape(c.secret)))
}

type liveClientSpec struct {
	confidential bool
	scopes       []string
}

type liveFlow struct {
	client                                   liveClient
	deviceCode, userCode                     string
	verificationURI, verificationURIComplete string
	expiresIn, interval                      int
	issuedAt, lastPoll                       time.Time
}

func (f *liveFlow) expiresAt() time.Time {
	return f.issuedAt.Add(time.Duration(f.expiresIn) * time.Second)
}

type liveBackend struct {
	t        *testing.T
	h        *liveHydra
	baseURL  string
	redis    *miniredis.Miniredis
	db       *database.HarukiToolboxDBManager
	logs     *liveLogSink
	http     *http.Client
	internal string
	runID    string

	alice, bob, admin liveUser

	failTokens atomic.Int32
	failed     atomic.Int32

	mu        sync.Mutex
	clients   []string
	secrets   []string
	tokenHits []time.Time
	responses []liveRecorded
}

type liveRecorded struct {
	path   string
	status int
	body   string
}

// liveLogSink collects the device-flow log lines.
type liveLogSink struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *liveLogSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *liveLogSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func newLiveBackend(t *testing.T, h *liveHydra) *liveBackend {
	t.Helper()
	b := &liveBackend{t: t, h: h, logs: &liveLogSink{}, runID: hex.EncodeToString(randomBytes(4)), http: &http.Client{Timeout: 30 * time.Second}}

	b.redis = miniredis.RunT(t)
	port, err := strconv.Atoi(b.redis.Port())
	if err != nil {
		t.Fatal(err)
	}
	redisManager := harukiRedis.NewRedisClient(harukiConfig.RedisConfig{Host: b.redis.Host(), Port: port}, liveSessionSignToken)
	t.Cleanup(func() { _ = redisManager.Close() })
	entClient := enttest.Open(t, "sqlite3", "file:hydra-live-"+b.runID+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = entClient.Close() })
	b.db = &database.HarukiToolboxDBManager{DB: entClient, Redis: redisManager}

	// Hydra subjects are the Kratos identity IDs: unique per run, so consent
	// sessions never mix across subtests or reruns.
	newUser := func(id, name string, role userSchema.Role) liveUser {
		user := liveUser{id: id, name: name, email: strings.ToLower(name) + "-" + b.runID + "@haruki-it.test", identityID: "it-" + b.runID + "-" + strings.ToLower(name), session: "session-" + b.runID + "-" + strings.ToLower(name)}
		entClient.User.Create().SetID(user.id).SetName(user.name).SetEmail(user.email).SetKratosIdentityID(user.identityID).SetRole(role).SaveX(t.Context())
		return user
	}
	b.alice = newUser("1001", "Alice", userSchema.RoleUser)
	b.bob = newUser("1002", "Bob", userSchema.RoleUser)
	b.admin = newUser("1003", "Carol", userSchema.RoleSuperAdmin)

	sessionHandler := harukiAPIHelper.NewSessionHandler(redisManager.Redis, "")
	sessionHandler.ConfigureIdentityProvider("kratos", "", "", "", "", false, false, time.Second, entClient)
	sessionHandler.ConfigureAuthProxy(true, "", liveAuthProxySecret, "", "", "", "", "")
	sessionHandler.ConfigureAuthProxySessionHeader(liveSessionHeader)

	app := fiber.New()
	app.Use(b.middleware)
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, DBManager: b.db, SessionHandler: sessionHandler}
	on := true
	if err := apiHelper.UpdateRuntimeConfig(harukiAPIHelper.RuntimeConfigUpdate{OAuth2DeviceFlowEnabled: &on}); err != nil {
		t.Fatalf("switch the device flow on: %v", err)
	}
	logger := harukiLogger.NewLogger("OAuth2DeviceLive", "DEBUG", b.logs)
	deviceFlow := oauth2Module.NewDeviceFlowConfig(liveDeviceFlowOptions(h, logger))
	b.internal = "internal-" + hex.EncodeToString(randomBytes(16))
	sum := sha256.Sum256([]byte(b.internal))
	internalAPI, err := oauth2Module.ParseInternalAPIConfig(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	api.RegisterRoutes(apiHelper, api.Dependencies{HydraConfig: h.config, OAuth2DeviceFlow: deviceFlow, OAuth2InternalAPI: internalAPI})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b.baseURL = "http://" + listener.Addr().String()
	go func() { _ = app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	reaperCtx, stopReaper := context.WithCancel(context.Background())
	waitReaper := oauth2Module.StartDeviceFlowReaper(reaperCtx, oauth2Module.DeviceFlowReaperOptions{Config: deviceFlow, HydraConfig: h.config, DBManager: b.db, Logger: logger})
	t.Cleanup(func() {
		stopReaper()
		waitReaper()
	})
	t.Cleanup(b.cleanupClients)
	t.Cleanup(b.assertNoSecretLeaks)
	return b
}

// liveDeviceFlowOptions are the production defaults of oauth2.device_flow,
// except the user code TTL (Hydra's) and the reaper's interval and grace.
func liveDeviceFlowOptions(h *liveHydra, logger *harukiLogger.Logger) oauth2Module.DeviceFlowConfigOptions {
	return oauth2Module.DeviceFlowConfigOptions{
		Enabled:         true,
		FrontendURL:     h.frontendURL,
		HydraIssuerURL:  h.issuerURL,
		UserCodeCharset: liveUserCodeCharset,
		UserCodeLength:  8,
		UserCodeTTL:     h.ttl,
		Timings: oauth2Module.DeviceFlowTimings{
			MinPollInterval:       5 * time.Second,
			ClaimTTL:              300 * time.Second,
			ApproveLease:          30 * time.Second,
			ApprovalTimeout:       15 * time.Second,
			MinRemainingToApprove: 30 * time.Second,
			MaxApproveAttempts:    3,
			RecordGrace:           1800 * time.Second,
			ReaperInterval:        liveReaperInterval,
			ReaperGrace:           liveReaperGrace,
		},
		Limits: oauth2Module.DeviceFlowLimits{
			AuthAttemptUnknownClientWarnPer10m: 6000,
			AuthAttemptClientWarnMultiplier:    10,
			AuthIssuedGlobalPer10m:             1200,
			AuthIssuedPublicPer10m:             600,
			AuthIssuedConfidentialPer10m:       600,
			AuthIssuedClientDefaultPer10m:      60,
			LookupUserPer10m:                   30,
			LookupFailUserPer10m:               5,
			LookupFailUserPerDay:               20,
			LookupFailGlobalPer10m:             150,
			DecisionUserPerDay:                 20,
			MaxSlowDown:                        30,
			MaxIntervalSeconds:                 60,
		},
		Logger: logger,
	}
}

// middleware records the device-facing and browser responses for the leak
// check, timestamps token requests, and injects 503s into the token endpoint.
func (b *liveBackend) middleware(c fiber.Ctx) error {
	// Fiber reuses the request buffers once the handler returns.
	path := strings.Clone(c.Path())
	if path == "/api/oauth2/token" {
		b.mu.Lock()
		b.tokenHits = append(b.tokenHits, time.Now())
		b.mu.Unlock()
		if b.failTokens.Add(-1) >= 0 {
			b.failed.Add(1)
			c.Set(fiber.HeaderCacheControl, "no-store")
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "temporarily_unavailable", "error_description": "injected by the live test"})
		}
		b.failTokens.Store(0)
	}
	err := c.Next()
	if strings.HasPrefix(path, "/api/oauth2/") || strings.HasPrefix(path, "/internal/") {
		b.mu.Lock()
		b.responses = append(b.responses, liveRecorded{path: path, status: c.Response().StatusCode(), body: string(c.Response().Body())})
		b.mu.Unlock()
	}
	return err
}

func (b *liveBackend) failNextTokenRequests(n int32) { b.failTokens.Store(n) }
func (b *liveBackend) injectedFailures() int         { return int(b.failed.Load()) }

func (b *liveBackend) tokenRequestTimes() []time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.tokenHits)
}

func (b *liveBackend) recordSecrets(values ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, value := range values {
		if value != "" {
			b.secrets = append(b.secrets, value)
		}
	}
}

func (b *liveBackend) request(method, rawURL, contentType, body string, header map[string]string) liveResponse {
	b.t.Helper()
	req, err := http.NewRequestWithContext(b.t.Context(), method, rawURL, strings.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range header {
		req.Header.Set(name, value)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return liveResponse{status: resp.StatusCode, header: resp.Header, body: raw}
}

// sessionHeaders are what Oathkeeper's header mutator injects for a signed-in
// Kratos session.
func (b *liveBackend) sessionHeaders(user liveUser) map[string]string {
	return map[string]string{
		"X-Auth-Proxy-Secret":   liveAuthProxySecret,
		"X-Kratos-Identity-Id":  user.identityID,
		"X-User-Name":           user.name,
		"X-User-Email":          user.email,
		"X-User-Email-Verified": "true",
		liveSessionHeader:       user.session,
	}
}

func (b *liveBackend) browserPost(user liveUser, path string, body any) liveResponse {
	b.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		b.t.Fatal(err)
	}
	header := b.sessionHeaders(user)
	header["Origin"] = b.h.frontendURL
	return b.request(http.MethodPost, b.baseURL+path, "application/json", string(encoded), header)
}

func (b *liveBackend) userRequest(user liveUser, method, path string) liveResponse {
	b.t.Helper()
	return b.request(method, b.baseURL+path, "", "", b.sessionHeaders(user))
}

func (b *liveBackend) adminRequest(method, path string, body any) liveResponse {
	b.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.request(method, b.baseURL+path, "application/json", string(encoded), b.sessionHeaders(b.admin))
}

func (b *liveBackend) bearerGet(token, path string) liveResponse {
	b.t.Helper()
	return b.request(http.MethodGet, b.baseURL+path, "", "", map[string]string{"Authorization": "Bearer " + token})
}

func (b *liveBackend) internalIntrospect(token string) liveResponse {
	b.t.Helper()
	return b.request(http.MethodPost, b.baseURL+oauth2Module.InternalIntrospectPath, "application/x-www-form-urlencoded", url.Values{"token": {token}}.Encode(), map[string]string{"Authorization": "Bearer " + b.internal})
}

// createClient registers a device-only client through the admin API, as an
// operator would.
func (b *liveBackend) createClient(spec liveClientSpec) liveClient {
	b.t.Helper()
	clientType := "public"
	if spec.confidential {
		clientType = "confidential"
	}
	clientID := "it-" + b.runID + "-" + clientType + "-" + hex.EncodeToString(randomBytes(3))
	grantTypes := []string{deviceGrantType}
	if slices.Contains(spec.scopes, "offline_access") {
		grantTypes = append(grantTypes, oauth2Module.HydraGrantTypeRefreshToken)
	}
	resp := b.adminRequest(http.MethodPost, "/api/admin/oauth-clients", map[string]any{
		"clientId":     clientID,
		"name":         "Live " + clientType + " client",
		"clientType":   clientType,
		"redirectUris": []string{},
		"scopes":       spec.scopes,
		"grantTypes":   grantTypes,
		"devicePolicy": map[string]any{"firstParty": false, "allowWrite": false},
	})
	expectStatus(b.t, resp, http.StatusOK)
	created := resp.updatedData(b.t)
	if created["deviceEnabled"] != true {
		b.t.Fatalf("created client is not device-enabled: %v", created)
	}
	b.mu.Lock()
	b.clients = append(b.clients, clientID)
	b.mu.Unlock()
	return liveClient{id: clientID, secret: stringField(created, "clientSecret"), confidential: spec.confidential}
}

// cleanupClients deletes the subtest's clients from Hydra; the cascade takes
// their consent sessions, tokens and device codes with them.
func (b *liveBackend) cleanupClients() {
	b.mu.Lock()
	clients := slices.Clone(b.clients)
	b.mu.Unlock()
	for _, clientID := range clients {
		req, err := http.NewRequest(http.MethodDelete, b.h.adminURL+"/admin/clients/"+url.PathEscape(clientID), nil)
		if err != nil {
			continue
		}
		if resp, err := b.h.http.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
}

// deviceAuth is H1 through the backend.
func (b *liveBackend) deviceAuth(client liveClient, scope, label string) (*liveFlow, liveResponse) {
	b.t.Helper()
	form := url.Values{"scope": {scope}}
	if label != "" {
		form.Set("device_label", label)
	}
	header := map[string]string{}
	if client.confidential {
		header["Authorization"] = client.basic()
	} else {
		form.Set("client_id", client.id)
	}
	resp := b.request(http.MethodPost, b.baseURL+"/api/oauth2/device/auth", "application/x-www-form-urlencoded", form.Encode(), header)
	if resp.status != http.StatusOK {
		return nil, resp
	}
	body := resp.json(b.t)
	flow := &liveFlow{
		client:                  client,
		deviceCode:              stringField(body, "device_code"),
		userCode:                stringField(body, "user_code"),
		verificationURI:         stringField(body, "verification_uri"),
		verificationURIComplete: stringField(body, "verification_uri_complete"),
		expiresIn:               intField(body, "expires_in"),
		interval:                intField(body, "interval"),
		issuedAt:                time.Now(),
	}
	b.recordSecrets(flow.deviceCode, flow.userCode, strings.ReplaceAll(flow.userCode, "-", ""))
	return flow, resp
}

// poll is H11 at the flow's interval; pollNow does not wait.
func (b *liveBackend) poll(flow *liveFlow) liveResponse {
	b.t.Helper()
	if !flow.lastPoll.IsZero() {
		time.Sleep(time.Until(flow.lastPoll.Add(time.Duration(flow.interval)*time.Second + 250*time.Millisecond)))
	}
	return b.pollNow(flow)
}

func (b *liveBackend) pollNow(flow *liveFlow) liveResponse {
	b.t.Helper()
	return b.pollAs(flow, flow.client)
}

func (b *liveBackend) pollAs(flow *liveFlow, client liveClient) liveResponse {
	b.t.Helper()
	form := url.Values{"grant_type": {deviceGrantType}, "device_code": {flow.deviceCode}}
	header := map[string]string{}
	if client.confidential {
		header["Authorization"] = client.basic()
	} else {
		form.Set("client_id", client.id)
	}
	resp := b.request(http.MethodPost, b.baseURL+"/api/oauth2/token", "application/x-www-form-urlencoded", form.Encode(), header)
	flow.lastPoll = time.Now()
	if resp.status == http.StatusBadRequest {
		if body := resp.json(b.t); body["error"] == "slow_down" {
			flow.interval = intField(body, "interval")
		}
	}
	return resp
}

// redeem polls until the tokens arrive.
func (b *liveBackend) redeem(flow *liveFlow) map[string]any {
	b.t.Helper()
	for time.Now().Before(flow.expiresAt()) {
		resp := b.poll(flow)
		if resp.status == http.StatusOK {
			assertNoStore(b.t, resp)
			return resp.json(b.t)
		}
		if resp.status != http.StatusBadRequest || resp.json(b.t)["error"] != "authorization_pending" {
			b.t.Fatalf("poll = %d %s", resp.status, resp.body)
		}
	}
	b.t.Fatal("the flow expired while redeeming")
	return nil
}

func (b *liveBackend) refresh(client liveClient, refreshToken string) liveResponse {
	b.t.Helper()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	header := map[string]string{}
	if client.confidential {
		header["Authorization"] = client.basic()
	} else {
		form.Set("client_id", client.id)
	}
	return b.request(http.MethodPost, b.baseURL+"/api/oauth2/token", "application/x-www-form-urlencoded", form.Encode(), header)
}

func (b *liveBackend) lookup(user liveUser, userCode string) liveResponse {
	b.t.Helper()
	resp := b.browserPost(user, "/api/oauth2/device/lookup", map[string]any{"userCode": userCode})
	if resp.status == http.StatusOK {
		b.recordSecrets(stringField(resp.updatedData(b.t), "flowHandle"))
	}
	return resp
}

func (b *liveBackend) approveHandle(user liveUser, flowHandle, userCode string) liveResponse {
	b.t.Helper()
	return b.browserPost(user, "/api/oauth2/device/approve", map[string]any{"flowHandle": flowHandle, "userCode": userCode, "acknowledged": true})
}

// approve looks the flow up and approves it, returning the consent request ID.
func (b *liveBackend) approve(user liveUser, flow *liveFlow) string {
	b.t.Helper()
	lookup := b.lookup(user, flow.userCode)
	expectStatus(b.t, lookup, http.StatusOK)
	approval := b.approveHandle(user, stringField(lookup.updatedData(b.t), "flowHandle"), flow.userCode)
	expectStatus(b.t, approval, http.StatusOK)
	consentRequestID := stringField(approval.updatedData(b.t), "consentRequestId")
	if consentRequestID == "" {
		b.t.Fatalf("approve answer = %s", approval.body)
	}
	return consentRequestID
}

// approveUserCode is approve for a goroutine: it reports instead of failing.
func (b *liveBackend) approveUserCode(user liveUser, userCode string) error {
	header := b.sessionHeaders(user)
	header["Origin"] = b.h.frontendURL
	post := func(path string, body map[string]any) (map[string]any, error) {
		encoded, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPost, b.baseURL+path, strings.NewReader(string(encoded)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for name, value := range header {
			req.Header.Set(name, value)
		}
		resp, err := b.http.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		var envelope struct {
			UpdatedData map[string]any `json:"updatedData"`
		}
		if err := json.UnmarshalRead(resp.Body, &envelope); err != nil || resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: HTTP %d (%v)", path, resp.StatusCode, err)
		}
		return envelope.UpdatedData, nil
	}
	card, err := post("/api/oauth2/device/lookup", map[string]any{"userCode": userCode})
	if err != nil {
		return err
	}
	b.recordSecrets(stringField(card, "flowHandle"))
	_, err = post("/api/oauth2/device/approve", map[string]any{"flowHandle": card["flowHandle"], "userCode": userCode, "acknowledged": true})
	return err
}

func (b *liveBackend) unredeemedCount() int {
	b.t.Helper()
	members, err := b.redis.ZMembers(b.db.Redis.KeyBuilder().BuildOAuth2DeviceUnredeemedKey())
	if err != nil && !errors.Is(err, miniredis.ErrKeyNotFound) {
		b.t.Fatal(err)
	}
	return len(members)
}

// assertNoSecretLeaks checks that no user code, wrapped or Hydra device code
// or flow handle reaches the logs or Redis (key names or values), that no
// response carries Hydra's device code, and that only a successful lookup
// carries a user code or flow handle.
func (b *liveBackend) assertNoSecretLeaks() {
	b.t.Helper()
	b.mu.Lock()
	secrets := slices.Clone(b.secrets)
	responses := slices.Clone(b.responses)
	b.mu.Unlock()
	var dump strings.Builder
	for _, key := range b.redis.Keys() {
		dump.WriteString(key + "\n")
		switch b.redis.Type(key) {
		case "hash":
			fields, _ := b.redis.HKeys(key)
			for _, field := range fields {
				dump.WriteString(field + "=" + b.redis.HGet(key, field) + "\n")
			}
		case "string":
			value, _ := b.redis.Get(key)
			dump.WriteString(value + "\n")
		case "zset":
			members, _ := b.redis.ZMembers(key)
			dump.WriteString(strings.Join(members, ",") + "\n")
		}
	}
	logs := b.logs.String()
	for _, where := range []struct{ name, text string }{{"the logs", logs}, {"Redis", dump.String()}} {
		for _, marker := range []string{"ory_dc_", "dfh_", "hdc_"} {
			if strings.Contains(where.text, marker) {
				b.t.Errorf("%s contain %q", where.name, marker)
			}
		}
		for _, secret := range secrets {
			if strings.Contains(where.text, secret) {
				b.t.Errorf("%s contain a user code, device code or flow handle", where.name)
			}
		}
	}
	for _, response := range responses {
		if strings.Contains(response.body, "ory_dc_") {
			b.t.Errorf("%s answered Hydra's device code", response.path)
		}
		issuing := response.path == "/api/oauth2/device/auth" || (response.path == "/api/oauth2/device/lookup" && response.status == http.StatusOK)
		if issuing {
			continue
		}
		for _, secret := range secrets {
			if strings.Contains(response.body, secret) {
				b.t.Errorf("%s answered a user code, device code or flow handle: %s", response.path, response.body)
			}
		}
	}
}

// --- Janitor ------------------------------------------------------------

// janitorFromCompose extracts the janitor's SQL and its grace and batch
// defaults from the production docker-compose.yml.
func janitorFromCompose(t *testing.T) (statement, grace, batch string) {
	t.Helper()
	raw, err := os.ReadFile("../../../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &compose); err != nil {
		t.Fatalf("parse docker-compose.yml: %v", err)
	}
	node, ok := compose.Services["hydra-device-janitor"]
	if !ok {
		t.Fatal("docker-compose.yml has no hydra-device-janitor service")
	}
	var janitor struct {
		Command     []string          `yaml:"command"`
		Environment map[string]string `yaml:"environment"`
	}
	if err := node.Decode(&janitor); err != nil || len(janitor.Command) != 1 {
		t.Fatalf("hydra-device-janitor is not a single list-form command (%v)", err)
	}
	_, rest, ok := strings.Cut(janitor.Command[0], "<<'SQL'\n")
	if !ok {
		t.Fatal("the janitor command has no <<'SQL' heredoc")
	}
	statement, _, ok = strings.Cut(rest, "\nSQL\n")
	if !ok {
		t.Fatal("the janitor heredoc has no SQL terminator at column 0")
	}
	composeDefault := func(name string) string {
		match := liveComposeDefault.FindStringSubmatch(janitor.Environment[name])
		if match == nil {
			t.Fatalf("janitor %s = %q has no ${…:-default}", name, janitor.Environment[name])
		}
		return match[1]
	}
	return statement, composeDefault("JANITOR_GRACE"), composeDefault("JANITOR_BATCH")
}

func janitorRows(t *testing.T, conn *pgx.Conn, clientID string) []string {
	t.Helper()
	rows, err := conn.Query(t.Context(), `SELECT device_code_signature FROM hydra_oauth2_device_auth_codes WHERE client_id = $1 ORDER BY requested_at, device_code_signature`, clientID)
	if err != nil {
		t.Fatal(err)
	}
	signatures, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return signatures
}

// quotePostgresLiteral is psql's :'name' substitution.
func quotePostgresLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// --- Helpers ------------------------------------------------------------

type liveResponse struct {
	status int
	header http.Header
	body   []byte
}

func (r liveResponse) json(t *testing.T) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(r.body, &decoded); err != nil {
		t.Fatalf("HTTP %d body %q is not a JSON object: %v", r.status, r.body, err)
	}
	return decoded
}

func (r liveResponse) updatedData(t *testing.T) map[string]any {
	t.Helper()
	data, _ := r.json(t)["updatedData"].(map[string]any)
	if data == nil {
		t.Fatalf("HTTP %d body has no updatedData object: %s", r.status, r.body)
	}
	return data
}

func expectStatus(t *testing.T, resp liveResponse, status int) {
	t.Helper()
	if resp.status != status {
		t.Fatalf("HTTP %d, want %d: %s", resp.status, status, resp.body)
	}
}

func expectOAuthError(t *testing.T, resp liveResponse, status int, code string) {
	t.Helper()
	if resp.status != status || resp.json(t)["error"] != code {
		t.Fatalf("HTTP %d %s, want %d %s", resp.status, resp.body, status, code)
	}
}

func expectBrowserCode(t *testing.T, resp liveResponse, status int, code string) {
	t.Helper()
	if resp.status != status || resp.updatedData(t)["code"] != code {
		t.Fatalf("HTTP %d %s, want %d with code %s", resp.status, resp.body, status, code)
	}
}

func assertNoStore(t *testing.T, resp liveResponse) {
	t.Helper()
	if resp.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", resp.header.Get("Cache-Control"))
	}
}

func authorizationEntry(t *testing.T, resp liveResponse, consentRequestID string) map[string]any {
	t.Helper()
	expectStatus(t, resp, http.StatusOK)
	entries, _ := resp.json(t)["updatedData"].([]any)
	for _, raw := range entries {
		if entry, _ := raw.(map[string]any); entry["consentRequestId"] == consentRequestID {
			return entry
		}
	}
	return nil
}

func stringField(values map[string]any, name string) string {
	value, _ := values[name].(string)
	return value
}

func intField(values map[string]any, name string) int {
	value, _ := values[name].(float64)
	return int(value)
}

func sortedKeys(values map[string]any) []string {
	return slices.Sorted(func(yield func(string) bool) {
		for key := range values {
			if !yield(key) {
				return
			}
		}
	})
}

func urlOrigin(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func waitUntil(t *testing.T, deadline time.Time) {
	t.Helper()
	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-time.After(time.Until(deadline)):
	}
}

// waitFor polls condition every second until it holds or the deadline passes.
func waitFor(t *testing.T, deadline time.Time, what string, condition func() bool) {
	t.Helper()
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		waitUntil(t, time.Now().Add(time.Second))
	}
}
