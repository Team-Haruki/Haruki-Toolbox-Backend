package oauth2

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDeviceAuthIssuesWrappedCode(t *testing.T) {
	env := newDeviceTestEnv(t)
	env.hydra.deviceAuth = func(url.Values) (int, any) {
		body := env.hydra.issueCode()
		body["interval"] = 2 // below the floor of 5 s
		return http.StatusOK, body
	}
	form := url.Values{
		"client_id":    {testPublicClientID},
		"scope":        {"user:read offline_access user:read"},
		"device_label": {"  Haruki-Client\u202e @\t home\u200b  server  "},
		"haruki_extra": {"dropped"},
	}
	resp := env.deviceAuth(form, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	assertNoStore(t, resp)
	body := resp.json(t)
	if _, hasHeader := body["Header"]; hasHeader {
		t.Fatal("Hydra's stray Header member was relayed")
	}
	hdc, _ := body["device_code"].(string)
	if _, ok := parseWrappedDeviceCode(hdc); !ok {
		t.Fatalf("device_code %q is not a wrapped code", hdc)
	}
	userCode := env.hydra.userCodes[0]
	formatted := formatDeviceUserCode(userCode)
	if body["user_code"] != formatted {
		t.Fatalf("user_code = %v, want %s", body["user_code"], formatted)
	}
	if body["verification_uri"] != testFrontendURL+"/device" || body["verification_uri_complete"] != testFrontendURL+"/device?user_code="+formatted {
		t.Fatalf("verification URIs = %v / %v", body["verification_uri"], body["verification_uri_complete"])
	}
	if body["expires_in"] != float64(testHydraDeviceTTL) || body["interval"] != float64(5) {
		t.Fatalf("expires_in = %v, interval = %v", body["expires_in"], body["interval"])
	}

	// Hydra only received client_id and the normalized scope.
	calls := env.hydra.callsTo(http.MethodPost, "/oauth2/device/auth")
	if len(calls) != 1 {
		t.Fatalf("Hydra device/auth calls = %d", len(calls))
	}
	sent, _ := url.ParseQuery(calls[0].Body)
	if len(sent) != 2 || sent.Get("client_id") != testPublicClientID || sent.Get("scope") != "offline_access user:read" {
		t.Fatalf("Hydra received %q", calls[0].Body)
	}

	flowID, found, err := env.store.flowIDForDeviceCode(t.Context(), hdc)
	if err != nil || !found {
		t.Fatalf("flow not indexed: %v", err)
	}
	keys := env.db.Redis.KeyBuilder()
	for name, want := range map[string]string{
		"v": "1", "cid": testPublicClientID, "ctype": "public", "scope": "offline_access user:read",
		"dlb": "Haruki-Client @ home server", "st": "pending", "ivl": "5", "lpoll": "0", "sdn": "0",
		"uch": keys.HashOAuth2DeviceIdentifier("uc", userCode),
		"crt": strconv.FormatInt(testDeviceEpoch.UnixMilli(), 10),
		"exp": strconv.FormatInt(testDeviceEpoch.Add(testHydraDeviceTTL*time.Second).UnixMilli(), 10),
	} {
		if got := env.field(flowID, name); got != want {
			t.Errorf("flow field %s = %q, want %q", name, got, want)
		}
	}
	if ttl := env.redis.TTL(env.flowKey(flowID)); ttl != (testHydraDeviceTTL+1800)*time.Second {
		t.Errorf("flow TTL = %s", ttl)
	}
	if ttl := env.redis.TTL(keys.BuildOAuth2DeviceUserCodeIndexKey(userCode)); ttl != testHydraDeviceTTL*time.Second {
		t.Errorf("user code index TTL = %s", ttl)
	}
	// The sealed code opens only with the wrapped code it was issued with.
	raw, _ := parseWrappedDeviceCode(hdc)
	opened, err := openHydraDeviceCode(raw, flowID, testPublicClientID, env.field(flowID, "wdc"))
	if err != nil || opened != env.hydra.deviceCodes[0] {
		t.Fatalf("sealed device code did not open: %v", err)
	}
	if !strings.Contains(env.logs.String(), "event=authorize fid="+flowID) {
		t.Fatalf("authorize event not logged: %s", env.logs.String())
	}
	env.assertNoSecretLeaks()
}

func TestDeviceAuthConfidentialClientUsesBasic(t *testing.T) {
	env := newDeviceTestEnv(t)
	// The Basic username and password are form-encoded (RFC 6749 §2.3.1).
	authorization := basicAuth(testConfidentialClientID, testConfidentialSecret)
	resp := env.deviceAuth(url.Values{"scope": {testDeviceScope}}, authorization)
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	calls := env.hydra.callsTo(http.MethodPost, "/oauth2/device/auth")
	sent, _ := url.ParseQuery(calls[0].Body)
	if sent.Get("client_id") != testConfidentialClientID || calls[0].Header.Get("Authorization") != authorization {
		t.Fatalf("Hydra received body %q, Authorization %q", calls[0].Body, calls[0].Header.Get("Authorization"))
	}
	hdc, _ := resp.json(t)["device_code"].(string)
	flowID, _, _ := env.store.flowIDForDeviceCode(t.Context(), hdc)
	if env.field(flowID, "ctype") != "confidential" {
		t.Fatalf("ctype = %q", env.field(flowID, "ctype"))
	}
}

func TestDeviceAuthInvalidRequest(t *testing.T) {
	cases := []struct {
		name          string
		contentType   string
		body          string
		authorization string
	}{
		{"json body", "application/json", `{"client_id":"haruki-client","scope":"user:read"}`, ""},
		{"no content type", "", "client_id=haruki-client&scope=user%3Aread", ""},
		{"body over 4 KiB", formURLEncodedMediaType, "client_id=haruki-client&scope=user%3Aread&device_label=" + strings.Repeat("a", 4096), ""},
		{"unparsable", formURLEncodedMediaType, "client_id=%zz&scope=user%3Aread", ""},
		{"missing client_id", formURLEncodedMediaType, "scope=user%3Aread", ""},
		{"form client_id differs from Basic", formURLEncodedMediaType, "client_id=haruki-client&scope=user%3Aread", basicAuth(testConfidentialClientID, testConfidentialSecret)},
		{"malformed Basic", formURLEncodedMediaType, "scope=user%3Aread", "Basic !!!"},
		{"bearer instead of Basic", formURLEncodedMediaType, "scope=user%3Aread", "Bearer ory_at_x"},
		{"audience", formURLEncodedMediaType, "client_id=haruki-client&scope=user%3Aread&audience=https%3A%2F%2Fx", ""},
		{"repeated scope", formURLEncodedMediaType, "client_id=haruki-client&scope=user%3Aread&scope=openid", ""},
		{"repeated client_id", formURLEncodedMediaType, "client_id=haruki-client&client_id=other&scope=user%3Aread", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceTestEnv(t)
			header := map[string]string{}
			if tc.authorization != "" {
				header["Authorization"] = tc.authorization
			}
			resp := env.do("/api/oauth2/device/auth", tc.contentType, tc.body, header)
			if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorInvalidRequest {
				t.Fatalf("status = %d, body %s; want 400 invalid_request", resp.Status, resp.Body)
			}
			assertNoStore(t, resp)
			if len(env.hydra.calls) != 0 {
				t.Fatalf("Hydra was called %d times", len(env.hydra.calls))
			}
			if keys := env.redis.Keys(); len(keys) != 0 {
				t.Fatalf("Redis was written: %v", keys)
			}
		})
	}
}

func TestDeviceAuthUnauthorizedClient(t *testing.T) {
	cases := []struct {
		name   string
		option deviceTestEnvOption
		setup  func(env *deviceTestEnv)
	}{
		{"startup switch off", func(o *DeviceFlowConfigOptions) { o.Enabled = false }, nil},
		{"runtime switch off", nil, func(env *deviceTestEnv) { env.gateOn.Store(false) }},
		{"not on the allowlist", func(o *DeviceFlowConfigOptions) { o.ClientAllowlist = []string{"other-client"} }, nil},
		{"client disabled", nil, func(env *deviceTestEnv) {
			env.hydra.setClient(testDeviceClient(testPublicClientID, "none", testDeviceScope, testDeviceGrantTypes, map[string]any{"haruki": map[string]any{"active": false}}))
		}},
		{"no device grant", nil, func(env *deviceTestEnv) {
			env.hydra.setClient(testDeviceClient(testPublicClientID, "none", testDeviceScope, []string{HydraGrantTypeAuthorizationCode, HydraGrantTypeRefreshToken}, nil))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var options []deviceTestEnvOption
			if tc.option != nil {
				options = append(options, tc.option)
			}
			env := newDeviceTestEnv(t, options...)
			if tc.setup != nil {
				tc.setup(env)
			}
			resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
			if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorUnauthorizedClient {
				t.Fatalf("status = %d, body %s; want 400 unauthorized_client", resp.Status, resp.Body)
			}
			assertNoStore(t, resp)
			if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/device/auth"); len(calls) != 0 {
				t.Fatal("Hydra device/auth was called")
			}
		})
	}
}

func TestDeviceAuthUnknownClientIsInvalidClient(t *testing.T) {
	env := newDeviceTestEnv(t)
	resp := env.deviceAuth(url.Values{"client_id": {"nobody"}, "scope": {testDeviceScope}}, "")
	if resp.Status != http.StatusUnauthorized || resp.oauthError(t) != oauthErrorInvalidClient {
		t.Fatalf("status = %d, body %s; want 401 invalid_client", resp.Status, resp.Body)
	}
	resp = env.deviceAuth(url.Values{"scope": {testDeviceScope}}, basicAuth("nobody", "secret"))
	if resp.Status != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("Basic unknown client: status = %d, WWW-Authenticate %q", resp.Status, resp.Header.Get("WWW-Authenticate"))
	}
}

// A flood naming unknown clients is only counted for a warning; it never
// consumes the issuance budget of real clients.
func TestDeviceAuthUnknownClientFloodDoesNotBlockKnownClient(t *testing.T) {
	env := newDeviceTestEnv(t, func(o *DeviceFlowConfigOptions) {
		o.Limits.AuthAttemptUnknownClientWarnPer10m = 5
		o.Limits.AuthIssuedPublicPer10m = 2
	})
	for i := range 20 {
		resp := env.deviceAuth(url.Values{"client_id": {"flood-" + strconv.Itoa(i%3)}, "scope": {testDeviceScope}}, "")
		if resp.Status != http.StatusUnauthorized {
			t.Fatalf("flood request %d: status = %d", i, resp.Status)
		}
	}
	if count := strings.Count(env.logs.String(), "event=auth_unknown_client_warn"); count != 1 {
		t.Fatalf("auth_unknown_client_warn logged %d times, want exactly once", count)
	}
	if strings.Contains(env.logs.String(), "flood-") {
		t.Fatal("an attacker-chosen client ID was logged")
	}
	if _, err := env.redis.Get(env.db.Redis.KeyBuilder().BuildOAuth2DeviceAuthIssuedPoolKey("public")); err == nil {
		t.Fatal("unknown clients consumed the public issuance pool")
	}
	env.issue(testPublicClientID)
	env.assertNoSecretLeaks()
}

// Public client IDs can be used by anyone, so exhausting the public pool must
// leave confidential clients unaffected.
func TestDeviceAuthPublicPoolExhaustionDoesNotBlockConfidential(t *testing.T) {
	env := newDeviceTestEnv(t, func(o *DeviceFlowConfigOptions) {
		o.Limits.AuthIssuedPublicPer10m = 2
		o.Limits.AuthIssuedConfidentialPer10m = 2
	})
	env.issue(testPublicClientID)
	env.issue(testPublicClientID)
	env.advance(time.Minute)
	resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
	if resp.Status != http.StatusTooManyRequests || resp.oauthError(t) != oauthErrorTemporarilyUnavailable {
		t.Fatalf("status = %d, body %s; want 429 temporarily_unavailable", resp.Status, resp.Body)
	}
	retryAfter, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || retryAfter < 1 || retryAfter > 600 {
		t.Fatalf("Retry-After = %q", resp.Header.Get("Retry-After"))
	}
	assertNoStore(t, resp)
	if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/device/auth"); len(calls) != 2 {
		t.Fatalf("Hydra device/auth calls = %d, want 2", len(calls))
	}
	env.issue(testConfidentialClientID)
	if !strings.Contains(env.logs.String(), "event=auth_issued_global_warn pool=public") {
		t.Fatalf("pool warning not logged: %s", env.logs.String())
	}
	env.assertNoSecretLeaks()
}

func TestDeviceAuthPerClientQuota(t *testing.T) {
	env := newDeviceTestEnv(t)
	env.hydra.setClient(testDeviceClient(testPublicClientID, "none", testDeviceScope, testDeviceGrantTypes,
		map[string]any{"haruki": map[string]any{"device": map[string]any{"max_codes_per_10m": 1}}}))
	env.issue(testPublicClientID)
	resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
	if resp.Status != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("status = %d, Retry-After %q; want 429", resp.Status, resp.Header.Get("Retry-After"))
	}
	// The refused request consumed nothing.
	if got, _ := env.redis.Get(env.db.Redis.KeyBuilder().BuildOAuth2DeviceAuthIssuedPoolKey("public")); got != "1" {
		t.Fatalf("public pool = %q, want 1", got)
	}
}

func TestDeviceAuthInvalidScope(t *testing.T) {
	cases := []struct {
		name        string
		clientID    string
		scope       string
		description string
	}{
		{"empty scope", testPublicClientID, "", ""},
		{"missing user:read", testPublicClientID, "openid offline_access", deviceScopeUserReadRequired},
		{"email", testPublicClientID, "user:read email", ""},
		{"not registered for the client", testConfidentialClientID, "user:read bindings:read", ""},
		{"write without allow_write", testPublicClientID, "user:read game-data:write", ""},
		{"write for a confidential client", testConfidentialClientID, "user:read game-data:write", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceTestEnv(t)
			form := url.Values{"scope": {tc.scope}}
			authorization := ""
			if tc.clientID == testConfidentialClientID {
				env.hydra.setClient(testDeviceClient(testConfidentialClientID, "client_secret_basic", "openid offline_access user:read game-data:write", testDeviceGrantTypes,
					map[string]any{"haruki": map[string]any{"device": map[string]any{"allow_write": true}}}))
				authorization = basicAuth(testConfidentialClientID, testConfidentialSecret)
			} else {
				form.Set("client_id", tc.clientID)
			}
			resp := env.deviceAuth(form, authorization)
			if resp.Status != http.StatusBadRequest || resp.oauthError(t) != oauthErrorInvalidScope {
				t.Fatalf("status = %d, body %s; want 400 invalid_scope", resp.Status, resp.Body)
			}
			if tc.description != "" && resp.json(t)["error_description"] != tc.description {
				t.Fatalf("error_description = %v", resp.json(t)["error_description"])
			}
			if calls := env.hydra.callsTo(http.MethodPost, "/oauth2/device/auth"); len(calls) != 0 {
				t.Fatal("Hydra device/auth was called")
			}
		})
	}
}

func TestDeviceAuthAllowsWriteForPublicClientWithAllowWrite(t *testing.T) {
	env := newDeviceTestEnv(t)
	env.hydra.setClient(testDeviceClient(testPublicClientID, "none", "user:read game-data:write", testDeviceGrantTypes,
		map[string]any{"haruki": map[string]any{"device": map[string]any{"allow_write": true}}}))
	resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {"user:read game-data:write"}}, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
}

// Hydra's non-200 answers are relayed with their status, body and
// WWW-Authenticate, and both reservations are given back.
func TestDeviceAuthHydraErrorPassthroughReleasesReservation(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   map[string]any
	}{
		{"wrong secret", http.StatusUnauthorized, map[string]any{"error": "invalid_client", "error_description": "Client authentication failed"}},
		{"invalid_scope", http.StatusBadRequest, map[string]any{"error": "invalid_scope", "error_description": "The requested scope is invalid"}},
		{"other 4xx", http.StatusBadRequest, map[string]any{"error": "invalid_request", "error_description": "hydra says no"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceTestEnv(t)
			env.hydra.deviceAuth = func(url.Values) (int, any) { return tc.status, tc.body }
			env.hydra.token = nil
			resp := env.deviceAuth(url.Values{"scope": {testDeviceScope}}, basicAuth(testConfidentialClientID, "wrong"))
			if resp.Status != tc.status || resp.oauthError(t) != tc.body["error"] {
				t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
			}
			if tc.status == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") == "" {
				t.Fatal("WWW-Authenticate was not relayed")
			}
			assertNoStore(t, resp)
			keys := env.db.Redis.KeyBuilder()
			for _, key := range []string{keys.BuildOAuth2DeviceAuthIssuedPoolKey("confidential"), keys.BuildOAuth2DeviceAuthIssuedClientKey(testConfidentialClientID)} {
				if env.redis.Exists(key) {
					t.Fatalf("reservation %s was not released", key)
				}
			}
		})
	}
}

func TestDeviceAuthServiceUnavailable(t *testing.T) {
	t.Run("runtime switch unreadable", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		env.gateErr.Store(true)
		resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
		if resp.Status != http.StatusServiceUnavailable || resp.oauthError(t) != oauthErrorTemporarilyUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
	t.Run("Hydra admin unreachable", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		env.hydra.clientLookupStatus = http.StatusInternalServerError
		resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
		if resp.Status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
	t.Run("Redis not configured", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		env.store.db = nil
		resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
		if resp.Status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
	})
	t.Run("Redis write fails after Hydra issued", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		env.hydra.deviceAuth = func(url.Values) (int, any) {
			env.redis.SetError("LOADING Redis is loading the dataset in memory")
			return http.StatusOK, env.hydra.issueCode()
		}
		resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
		if resp.Status != http.StatusServiceUnavailable || resp.oauthError(t) != oauthErrorTemporarilyUnavailable {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
		env.redis.SetError("")
		env.assertNoSecretLeaks()
	})
}

func TestDeviceAuthServerErrorOnDrift(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(body map[string]any)
		event string
	}{
		{"user code outside the charset", func(b map[string]any) { b["user_code"] = "AEIOUAEI" }, "charset_mismatch"},
		{"user code not normalized", func(b map[string]any) { b["user_code"] = "bcdfghjk" }, "charset_mismatch"},
		{"user code too short", func(b map[string]any) { b["user_code"] = "BCDFGHJ" }, "charset_mismatch"},
		{"expires_in beyond the configured TTL", func(b map[string]any) { b["expires_in"] = 3600 }, "ttl_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDeviceTestEnv(t)
			env.hydra.deviceAuth = func(url.Values) (int, any) {
				body := env.hydra.issueCode()
				tc.edit(body)
				return http.StatusOK, body
			}
			resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
			if resp.Status != http.StatusInternalServerError || resp.oauthError(t) != oauthErrorServerError {
				t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
			}
			if !strings.Contains(env.logs.String(), "event="+tc.event) {
				t.Fatalf("%s not logged: %s", tc.event, env.logs.String())
			}
		})
	}
	t.Run("user code collision", func(t *testing.T) {
		env := newDeviceTestEnv(t)
		env.issue(testPublicClientID)
		env.hydra.deviceAuth = func(url.Values) (int, any) {
			body := env.hydra.issueCode()
			body["user_code"] = env.hydra.userCodes[0]
			return http.StatusOK, body
		}
		before := len(env.redis.Keys())
		resp := env.deviceAuth(url.Values{"client_id": {testPublicClientID}, "scope": {testDeviceScope}}, "")
		if resp.Status != http.StatusInternalServerError || !strings.Contains(env.logs.String(), "reason=uc_collision") {
			t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
		}
		// Neither the flow nor its dc index was written (the attempt counter
		// is the only new key).
		if after := len(env.redis.Keys()); after != before {
			t.Fatalf("Redis keys %d -> %d after a collision", before, after)
		}
		env.assertNoSecretLeaks()
	})
}

// The 5 s client cache serves repeated lookups, including 404s.
func TestDeviceAuthCachesClientLookups(t *testing.T) {
	env := newDeviceTestEnv(t)
	for range 3 {
		env.deviceAuth(url.Values{"client_id": {"nobody"}, "scope": {testDeviceScope}}, "")
		env.issue(testPublicClientID)
	}
	if calls := env.hydra.callsTo(http.MethodGet, "/admin/clients/nobody"); len(calls) != 1 {
		t.Fatalf("unknown client lookups = %d, want 1", len(calls))
	}
	if calls := env.hydra.callsTo(http.MethodGet, "/admin/clients/"+testPublicClientID); len(calls) != 1 {
		t.Fatalf("known client lookups = %d, want 1", len(calls))
	}
	env.advance(6 * time.Second)
	env.issue(testPublicClientID)
	if calls := env.hydra.callsTo(http.MethodGet, "/admin/clients/"+testPublicClientID); len(calls) != 2 {
		t.Fatalf("known client lookups after 6 s = %d, want 2", len(calls))
	}
}
