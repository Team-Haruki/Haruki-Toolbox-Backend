package oauth2

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"

	"github.com/gofiber/fiber/v3"
)

const testConsentRequestID = "consent-request-1"

var testTokenResponse = map[string]any{"access_token": "ory_at_issuedaccesstoken", "refresh_token": "ory_rt_issuedrefreshtoken", "token_type": "bearer", "expires_in": 3600}

func hydraTokenAnswer(status int, body map[string]any) func(url.Values, http.Header) (int, any) {
	return func(url.Values, http.Header) (int, any) { return status, body }
}

func hydraOAuthError(code string) map[string]any {
	return map[string]any{"error": code, "error_description": "hydra: " + code}
}

// The non-device branch must forward exactly what handleHydraPublicProxy
// forwards and relay exactly what it relays.
func TestTokenShimNonDeviceGrantVerbatim(t *testing.T) {
	type observed struct {
		method, path, query, body, auth, contentType, accept string
	}
	var seen []observed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(raw)
		seen = append(seen, observed{r.Method, r.URL.Path, r.URL.RawQuery, string(raw), r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("Accept")})
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		w.Header().Set("Cache-Control", "private")
		w.Header().Set("WWW-Authenticate", `Basic realm="hydra"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"verbatim"}`))
	}))
	t.Cleanup(server.Close)
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{PublicURL: server.URL})

	shim := fiber.New()
	shim.Post("/api/oauth2/token", handleHydraTokenEndpoint(nil, hydraConfig, DeviceFlowConfig{}, newDeviceFlowStore(nil)))
	proxy := fiber.New()
	proxy.Post("/api/oauth2/token", handleHydraPublicProxy(hydraConfig, "/oauth2/token"))

	requests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"refresh_token", formURLEncodedMediaType, "grant_type=refresh_token&refresh_token=ory_rt_x&scope=openid+user%3Aread"},
		{"authorization_code", formURLEncodedMediaType, "grant_type=authorization_code&code=abc&redirect_uri=https%3A%2F%2Frp.example.com%2Fcb"},
		{"device grant in a JSON body", "application/json", `{"grant_type":"urn:ietf:params:oauth:grant-type:device_code","device_code":"hdc_x"}`},
		{"unparsable form", formURLEncodedMediaType, "grant_type=%zz"},
	}
	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			run := func(app *fiber.App) (*http.Response, string) {
				req := httptest.NewRequest(http.MethodPost, "/api/oauth2/token?x=1&y=two", strings.NewReader(tc.body))
				req.Header.Set("Content-Type", tc.contentType)
				req.Header.Set("Authorization", "Basic dGVzdDp0ZXN0")
				req.Header.Set("Accept", "application/json")
				resp, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				raw := make([]byte, 4096)
				n, _ := resp.Body.Read(raw)
				return resp, string(raw[:n])
			}
			seen = nil
			shimResp, shimBody := run(shim)
			proxyResp, proxyBody := run(proxy)
			if len(seen) != 2 || seen[0] != seen[1] {
				t.Fatalf("forwarded requests differ: %+v", seen)
			}
			if seen[0].body != tc.body || seen[0].query != "x=1&y=two" || seen[0].path != "/oauth2/token" || seen[0].auth != "Basic dGVzdDp0ZXN0" {
				t.Fatalf("forwarded request = %+v", seen[0])
			}
			if shimResp.StatusCode != proxyResp.StatusCode || shimBody != proxyBody {
				t.Fatalf("responses differ: %d %q vs %d %q", shimResp.StatusCode, shimBody, proxyResp.StatusCode, proxyBody)
			}
			for _, name := range []string{"Content-Type", "Cache-Control", "Pragma", "WWW-Authenticate"} {
				if shimResp.Header.Get(name) != proxyResp.Header.Get(name) {
					t.Fatalf("%s differs: %q vs %q", name, shimResp.Header.Get(name), proxyResp.Header.Get(name))
				}
			}
		})
	}
}

func TestTokenShimEarlyPollSlowDownWithoutHydraCall(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)

	if resp := env.poll(hdc, testPublicClientID); resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorAuthorizationPending {
		t.Fatalf("first poll: status = %d, body %s", resp.Status, resp.Body)
	}
	env.advance(time.Second)
	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorSlowDown {
		t.Fatalf("early poll: status = %d, body %s", resp.Status, resp.Body)
	}
	assertNoStore(t, resp)
	body := resp.json(t)
	if body["interval"] != float64(10) || body["error_description"] != deviceSlowDownText {
		t.Fatalf("slow_down body = %v", body)
	}
	if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/token"); len(calls) != 1 {
		t.Fatalf("Hydra token calls = %d, want 1", len(calls))
	}
	// The raised interval sticks: 9 s later is still early (10 s - 1 s tolerance
	// counts from the last poll), 10 s later is not.
	env.advance(8 * time.Second)
	if resp := env.poll(hdc, testPublicClientID); resp.oauthError(t) != oauthErrorSlowDown {
		t.Fatalf("poll after 8 s: %s", resp.Body)
	}
	env.advance(14 * time.Second)
	if resp := env.poll(hdc, testPublicClientID); resp.oauthError(t) != oauthErrorAuthorizationPending {
		t.Fatalf("poll after 14 s: %s", resp.Body)
	}
	if env.field(flowID, "sdn") != "2" || env.field(flowID, "ivl") != "15" {
		t.Fatalf("sdn = %s, ivl = %s", env.field(flowID, "sdn"), env.field(flowID, "ivl"))
	}
	if !strings.Contains(env.logs.String(), "event=poll_slow_down fid="+flowID) {
		t.Fatal("poll_slow_down not logged")
	}
	env.assertNoSecretLeaks()
}

func TestTokenShimTooManySlowDownsFailsFlow(t *testing.T) {
	env := newDeviceTestEnv(t, func(o *DeviceFlowConfigOptions) { o.Limits.MaxSlowDown = 2 })
	hdc, flowID := env.issue(testPublicClientID)
	env.poll(hdc, testPublicClientID)
	for range 3 {
		if resp := env.poll(hdc, testPublicClientID); resp.oauthError(t) != oauthErrorSlowDown {
			t.Fatalf("expected slow_down, got %s", resp.Body)
		}
	}
	if env.field(flowID, "st") != deviceFlowStateFailed {
		t.Fatalf("state = %s, want failed", env.field(flowID, "st"))
	}
	env.advance(time.Minute)
	if resp := env.poll(hdc, testPublicClientID); resp.oauthError(t) != oauthErrorExpiredToken {
		t.Fatalf("poll of a failed flow: %s", resp.Body)
	}
}

// Before Hydra authenticated the client, a wrong secret must not learn that
// the flow was denied.
func TestTokenShimHydra401PassthroughWithoutStateReveal(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testConfidentialClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.setFlow(flowID, "st", deviceFlowStateDenied, "dres", "user_denied")
	env.hydra.token = hydraTokenAnswer(http.StatusUnauthorized, map[string]any{"error": "invalid_client", "error_description": "Client authentication failed"})

	form := url.Values{"grant_type": {HydraGrantTypeDeviceCode}, "device_code": {hdc}}
	resp := env.do("/api/oauth2/token", formURLEncodedMediaType, form.Encode(), map[string]string{"Authorization": basicAuth(testConfidentialClientID, "wrong")})
	if resp.Status != http.StatusUnauthorized || resp.oauthError(t) != oauthErrorInvalidClient {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if body := resp.json(t); len(body) != 2 || body["error_description"] != "Client authentication failed" {
		t.Fatalf("body was rewritten: %s", resp.Body)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("WWW-Authenticate was not relayed")
	}
	assertNoStore(t, resp)
	if strings.Contains(string(resp.Body), oauthErrorAccessDenied) {
		t.Fatal("the denial was revealed before client authentication")
	}
	if env.field(flowID, "st") != deviceFlowStateDenied || !env.inUnredeemed(flowID) {
		t.Fatal("a 401 changed the flow")
	}
	if calls := env.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent"); len(calls) != 0 {
		t.Fatal("a 401 triggered a revocation")
	}
	// The request reached Hydra with the flow's client ID and the caller's
	// Authorization, never the wrapped code.
	call := env.hydra.callsTo(http.MethodPost, "/oauth2/token")[0]
	sent, _ := url.ParseQuery(call.Body)
	if sent.Get("client_id") != testConfidentialClientID || !strings.HasPrefix(sent.Get("device_code"), "ory_dc_") || call.Header.Get("Authorization") != basicAuth(testConfidentialClientID, "wrong") {
		t.Fatalf("Hydra received %q", call.Body)
	}
	env.assertNoSecretLeaks()
}

func TestTokenShimHydra200AfterDenyRevokesAndDenies(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.setFlow(flowID, "st", deviceFlowStateDenied, "dres", "not_initiated_by_me")
	env.hydra.token = hydraTokenAnswer(http.StatusOK, testTokenResponse)

	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorAccessDenied {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if strings.Contains(string(resp.Body), "ory_at_") || strings.Contains(string(resp.Body), "ory_rt_") {
		t.Fatal("tokens were handed out for a denied flow")
	}
	revokes := env.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent")
	if len(revokes) != 1 || revokes[0].RawQuery != "consent_request_id="+testConsentRequestID {
		t.Fatalf("revocations = %+v", revokes)
	}
	if env.field(flowID, "st") != deviceFlowStateDenied || env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	if !strings.Contains(env.logs.String(), "event=token_after_terminal fid="+flowID) {
		t.Fatal("token_after_terminal not logged")
	}
	env.assertNoSecretLeaks()
}

// An approved flow whose tokens expired unredeemed keeps its consent session
// in the unredeemed set; the reaper revokes it after exp + grace.
func TestSettleExpiredTokenKeepsUnredeemedForReaper(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.hydra.token = hydraTokenAnswer(http.StatusBadRequest, hydraOAuthError(oauthErrorExpiredToken))

	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorExpiredToken {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if env.field(flowID, "st") != deviceFlowStateExpired || !env.inUnredeemed(flowID) {
		t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
	}
	if calls := env.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent"); len(calls) != 0 {
		t.Fatal("settle revoked; that is the reaper's job")
	}

	reaper := &deviceFlowReaper{store: env.store, hydraConfig: env.hydraConfig, logger: env.logger, grace: time.Minute}
	env.advance(testHydraDeviceTTL*time.Second + 30*time.Second)
	reaper.runOnce(t.Context())
	if !env.inUnredeemed(flowID) {
		t.Fatal("the reaper acted before exp + grace")
	}
	env.advance(31 * time.Second)
	reaper.runOnce(t.Context())
	revokes := env.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent")
	if len(revokes) != 1 || revokes[0].RawQuery != "consent_request_id="+testConsentRequestID {
		t.Fatalf("reaper revocations = %+v", revokes)
	}
	if env.inUnredeemed(flowID) || env.field(flowID, "st") != deviceFlowStateExpired {
		t.Fatal("the reaped flow was not settled")
	}
	if !strings.Contains(env.logs.String(), "event=reaped fid="+flowID) {
		t.Fatal("reaped not logged")
	}
	env.assertNoSecretLeaks()
}

func TestTokenShimIssuesTokenForApprovedFlow(t *testing.T) {
	for _, state := range []string{deviceFlowStateApproving, deviceFlowStateApproved, deviceFlowStateUnconfirmed} {
		t.Run(state, func(t *testing.T) {
			env := newDeviceTestEnv(t)
			hdc, flowID := env.issue(testPublicClientID)
			env.approve(flowID, state, testConsentRequestID)
			env.hydra.token = hydraTokenAnswer(http.StatusOK, testTokenResponse)

			resp := env.poll(hdc, testPublicClientID)
			if resp.Status != http.StatusOK || resp.json(t)["access_token"] != "ory_at_issuedaccesstoken" {
				t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
			}
			assertNoStore(t, resp)
			if env.field(flowID, "st") != deviceFlowStateIssued || env.field(flowID, "ist") == "" || env.inUnredeemed(flowID) {
				t.Fatalf("state = %s, unredeemed = %v", env.field(flowID, "st"), env.inUnredeemed(flowID))
			}
			if ttl := env.redis.TTL(env.flowKey(flowID)); ttl != 300*time.Second {
				t.Fatalf("issued flow TTL = %s", ttl)
			}
			if ttl := env.redis.TTL(env.db.Redis.KeyBuilder().BuildOAuth2DeviceCodeIndexKey(hdc)); ttl != 300*time.Second {
				t.Fatalf("issued dc index TTL = %s", ttl)
			}
			logs, err := env.db.DB.SystemLog.Query().Where(systemlog.ActionEQ(deviceAuditActionTokenIssued)).All(t.Context())
			if err != nil || len(logs) != 1 {
				t.Fatalf("token_issued audit rows = %d (%v)", len(logs), err)
			}
			entry := logs[0]
			if entry.ActorType != systemlog.ActorTypeAnonymous || entry.TargetID == nil || *entry.TargetID != "4242" ||
				entry.Metadata["deviceFlowID"] != flowID || entry.Metadata["clientID"] != testPublicClientID {
				t.Fatalf("audit entry = %+v", entry)
			}
			env.assertNoSecretLeaks()
		})
	}
}

// One case per row of the ory-suite-usage §10.5.4 table not covered by a dedicated test.
func TestTokenShimSettleTable(t *testing.T) {
	cases := []struct {
		name           string
		state          string
		consentID      string
		clientDisabled bool
		pastExpiry     bool
		hydraStatus    int
		hydraBody      map[string]any
		wantStatus     int
		wantError      string
		wantState      string
		wantUnredeemed bool
		wantRevoke     bool
	}{
		{"200 approved disabled client", deviceFlowStateApproved, testConsentRequestID, true, false, 200, testTokenResponse, 400, oauthErrorAccessDenied, deviceFlowStateDenied, false, true},
		{"200 after expiry", deviceFlowStateExpired, testConsentRequestID, false, false, 200, testTokenResponse, 400, oauthErrorExpiredToken, deviceFlowStateExpired, false, true},
		{"200 after failure", deviceFlowStateFailed, testConsentRequestID, false, false, 200, testTokenResponse, 400, oauthErrorExpiredToken, deviceFlowStateFailed, false, true},
		{"pending and denied", deviceFlowStateDenied, "", false, false, 400, hydraOAuthError(oauthErrorAuthorizationPending), 400, oauthErrorAccessDenied, deviceFlowStateDenied, false, false},
		{"pending and failed", deviceFlowStateFailed, "", false, false, 400, hydraOAuthError(oauthErrorAuthorizationPending), 400, oauthErrorExpiredToken, deviceFlowStateFailed, false, false},
		{"pending past expiry", deviceFlowStatePending, "", false, true, 400, hydraOAuthError(oauthErrorAuthorizationPending), 400, oauthErrorExpiredToken, deviceFlowStateExpired, false, false},
		{"pending claimed past expiry", deviceFlowStateClaimed, "", false, true, 400, hydraOAuthError(oauthErrorAuthorizationPending), 400, oauthErrorExpiredToken, deviceFlowStateExpired, false, false},
		{"pending approved past expiry keeps unredeemed", deviceFlowStateApproved, testConsentRequestID, false, true, 400, hydraOAuthError(oauthErrorAuthorizationPending), 400, oauthErrorExpiredToken, deviceFlowStateExpired, true, false},
		{"pending otherwise passes through", deviceFlowStateClaimed, "", false, false, 400, hydraOAuthError(oauthErrorAuthorizationPending), 400, oauthErrorAuthorizationPending, deviceFlowStateClaimed, false, false},
		{"pending approved passes through", deviceFlowStateApproved, testConsentRequestID, false, false, 400, hydraOAuthError(oauthErrorAuthorizationPending), 400, oauthErrorAuthorizationPending, deviceFlowStateApproved, true, false},
		{"expired_token pending", deviceFlowStatePending, "", false, false, 400, hydraOAuthError(oauthErrorExpiredToken), 400, oauthErrorExpiredToken, deviceFlowStateExpired, false, false},
		{"invalid_grant denied", deviceFlowStateDenied, testConsentRequestID, false, false, 400, hydraOAuthError(oauthErrorInvalidGrant), 400, oauthErrorAccessDenied, deviceFlowStateDenied, true, false},
		{"invalid_grant approved disabled client", deviceFlowStateApproved, testConsentRequestID, true, false, 400, hydraOAuthError(oauthErrorInvalidGrant), 400, oauthErrorAccessDenied, deviceFlowStateDenied, false, false},
		{"invalid_grant expired", deviceFlowStateExpired, testConsentRequestID, false, false, 400, hydraOAuthError(oauthErrorInvalidGrant), 400, oauthErrorExpiredToken, deviceFlowStateExpired, true, false},
		{"invalid_grant approved passes through", deviceFlowStateUnconfirmed, testConsentRequestID, false, false, 400, hydraOAuthError(oauthErrorInvalidGrant), 400, oauthErrorInvalidGrant, deviceFlowStateExpired, false, false},
		{"invalid_grant otherwise passes through", deviceFlowStatePending, "", false, false, 400, hydraOAuthError(oauthErrorInvalidGrant), 400, oauthErrorInvalidGrant, deviceFlowStatePending, false, false},
		{"other Hydra answer passes through", deviceFlowStateApproved, testConsentRequestID, false, false, 500, hydraOAuthError("server_error"), 500, "server_error", deviceFlowStateApproved, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceTestEnv(t)
			hdc, flowID := env.issue(testPublicClientID)
			if tc.consentID != "" {
				env.approve(flowID, tc.state, tc.consentID)
			}
			env.setFlow(flowID, "st", tc.state)
			if tc.clientDisabled {
				env.hydra.setClient(testDeviceClient(testPublicClientID, "none", testDeviceScope, testDeviceGrantTypes, map[string]any{"haruki": map[string]any{"active": false}}))
				env.advance(6 * time.Second) // past the client cache
			}
			if tc.pastExpiry {
				env.advance(testHydraDeviceTTL * time.Second)
			}
			env.hydra.token = hydraTokenAnswer(tc.hydraStatus, tc.hydraBody)

			resp := env.poll(hdc, testPublicClientID)
			if resp.Status != tc.wantStatus || resp.oauthError(t) != tc.wantError {
				t.Fatalf("status = %d, body %s; want %d %s", resp.Status, resp.Body, tc.wantStatus, tc.wantError)
			}
			assertNoStore(t, resp)
			if got := env.field(flowID, "st"); got != tc.wantState {
				t.Fatalf("state = %s, want %s", got, tc.wantState)
			}
			if got := env.inUnredeemed(flowID); got != tc.wantUnredeemed {
				t.Fatalf("unredeemed = %v, want %v", got, tc.wantUnredeemed)
			}
			revokes := env.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent")
			if (len(revokes) > 0) != tc.wantRevoke {
				t.Fatalf("revocations = %d, want %v", len(revokes), tc.wantRevoke)
			}
			if strings.Contains(string(resp.Body), "ory_at_") && tc.wantStatus != 200 {
				t.Fatal("a withheld token leaked")
			}
			env.assertNoSecretLeaks()
		})
	}
}

func TestTokenShimLocalErrors(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, _ := env.issue(testPublicClientID)
	otherHDC, _ := newWrappedDeviceCode()
	cases := []struct {
		name          string
		form          url.Values
		authorization string
		wantError     string
	}{
		{"no client", url.Values{"device_code": {hdc}}, "", oauthErrorInvalidRequest},
		{"no device_code", url.Values{"client_id": {testPublicClientID}}, "", oauthErrorInvalidRequest},
		{"form client differs from Basic", url.Values{"client_id": {testPublicClientID}, "device_code": {hdc}}, basicAuth(testConfidentialClientID, testConfidentialSecret), oauthErrorInvalidRequest},
		{"repeated device_code", url.Values{"client_id": {testPublicClientID}, "device_code": {hdc, hdc}}, "", oauthErrorInvalidRequest},
		{"Hydra's own code", url.Values{"client_id": {testPublicClientID}, "device_code": {"ory_dc_0000000000000000000000000000000000000001"}}, "", oauthErrorInvalidGrant},
		{"truncated wrapped code", url.Values{"client_id": {testPublicClientID}, "device_code": {hdc[:20]}}, "", oauthErrorInvalidGrant},
		{"unknown wrapped code", url.Values{"client_id": {testPublicClientID}, "device_code": {otherHDC}}, "", oauthErrorInvalidGrant},
		{"code of another client", url.Values{"device_code": {hdc}}, basicAuth(testConfidentialClientID, testConfidentialSecret), oauthErrorInvalidGrant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"grant_type": {HydraGrantTypeDeviceCode}}
			for k, v := range tc.form {
				form[k] = v
			}
			header := map[string]string{}
			if tc.authorization != "" {
				header["Authorization"] = tc.authorization
			}
			resp := env.do("/api/oauth2/token", formURLEncodedMediaType, form.Encode(), header)
			if resp.Status != http.StatusBadRequest || resp.oauthError(t) != tc.wantError {
				t.Fatalf("status = %d, body %s; want 400 %s", resp.Status, resp.Body, tc.wantError)
			}
			assertNoStore(t, resp)
		})
	}
	if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/token"); len(calls) != 0 {
		t.Fatalf("local errors reached Hydra %d times", len(calls))
	}
	env.assertNoSecretLeaks()
}

func TestTokenShimSwitchOffIsExpiredToken(t *testing.T) {
	t.Run("runtime switch off", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, _ := env.issue(testPublicClientID)
		env.gateOn.Store(false)
		resp := env.poll(hdc, testPublicClientID)
		if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorExpiredToken {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
	t.Run("runtime switch unreadable", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, _ := env.issue(testPublicClientID)
		env.gateErr.Store(true)
		resp := env.poll(hdc, testPublicClientID)
		if resp.Status != http.StatusServiceUnavailable || resp.oauthError(t) != oauthErrorTemporarilyUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
	t.Run("startup switch off", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, _ := env.issue(testPublicClientID)
		disabled := testDeviceFlowOptions(env.logger)
		disabled.Enabled = false
		env.app.Post("/off/token", handleHydraTokenEndpoint(env.apiHelper, env.hydraConfig, NewDeviceFlowConfig(disabled), env.store))
		form := url.Values{"grant_type": {HydraGrantTypeDeviceCode}, "device_code": {hdc}, "client_id": {testPublicClientID}}
		resp := env.do("/off/token", formURLEncodedMediaType, form.Encode(), nil)
		if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorExpiredToken {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
	t.Run("Redis unavailable", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		hdc, _ := env.issue(testPublicClientID)
		env.redis.SetError("LOADING Redis is loading the dataset in memory")
		resp := env.poll(hdc, testPublicClientID)
		if resp.Status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
}

// An approved flow needs the client's enabled state; when Hydra admin cannot
// be reached the shim answers 503 without forwarding the poll.
func TestTokenShimApprovedPrefetchFailureIs503(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.advance(6 * time.Second)
	env.hydra.clientLookupStatus = http.StatusInternalServerError
	resp := env.poll(hdc, testPublicClientID)
	if resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/token"); len(calls) != 0 {
		t.Fatal("the poll was forwarded")
	}
}

// A settle that keeps failing after Hydra issued tokens still hands them out
// for an approved flow; the flow stays unredeemed for the reaper.
func TestTokenShimSettleFailureStillHandsOutTokens(t *testing.T) {
	env := newDeviceTestEnv(t)
	hdc, flowID := env.issue(testPublicClientID)
	env.approve(flowID, deviceFlowStateApproved, testConsentRequestID)
	env.hydra.token = func(url.Values, http.Header) (int, any) {
		env.redis.SetError("LOADING Redis is loading the dataset in memory")
		return http.StatusOK, testTokenResponse
	}
	resp := env.poll(hdc, testPublicClientID)
	env.redis.SetError("")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	if !strings.Contains(env.logs.String(), "event=settle_failed fid="+flowID) {
		t.Fatal("settle_failed not logged")
	}
	if !env.inUnredeemed(flowID) || env.field(flowID, "st") != deviceFlowStateApproved {
		t.Fatal("an unsettled flow must stay unredeemed for the reaper")
	}
}
