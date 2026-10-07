package oauth2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	json "encoding/json/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

const (
	testInternalToken        = "internal-api-token-0123456789abcdef"
	testIntrospectClientID   = "haruki-client"
	testIntrospectIdentityID = "kratos-identity-1"
	testIntrospectUserID     = "12345"
	testIntrospectUserName   = "Alice Example"
	testIntrospectUserEmail  = "alice@example.test"
	// Access tokens carry the ory_at_ prefix so log redaction can be checked.
	testActiveAccessToken = "ory_at_activeAccessToken0123456789"
)

// introspectLogBuffer is a goroutine-safe log sink.
type introspectLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *introspectLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *introspectLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeIntrospectHydra serves Hydra admin introspection by token and client
// lookups by ID; a client missing from clients answers 404.
type fakeIntrospectHydra struct {
	mu             sync.Mutex
	tokens         map[string]map[string]any
	clients        map[string]map[string]any
	introspectFail bool
	introspections int
}

func (f *fakeIntrospectHydra) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/admin/oauth2/introspect":
		f.introspections++
		if f.introspectFail {
			writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			return
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("token_type_hint") != "access_token" {
			writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		if body, ok := f.tokens[r.PostForm.Get("token")]; ok {
			writeFakeHydraJSON(w, http.StatusOK, body)
			return
		}
		writeFakeHydraJSON(w, http.StatusOK, map[string]any{"active": false})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/admin/clients/"):
		if client, ok := f.clients[strings.TrimPrefix(r.URL.Path, "/admin/clients/")]; ok {
			writeFakeHydraJSON(w, http.StatusOK, client)
			return
		}
		writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "Unable to locate the resource"})
	default:
		writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
}

func (f *fakeIntrospectHydra) setToken(token string, body map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[token] = body
}

func (f *fakeIntrospectHydra) setClient(clientID string, client map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if client == nil {
		delete(f.clients, clientID)
		return
	}
	f.clients[clientID] = client
}

func (f *fakeIntrospectHydra) introspectionCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.introspections
}

// activeTokenBody is Hydra's introspection of an active device-flow access
// token, raw fields included, so tests can assert none of them leak.
func activeTokenBody(subject, clientID, scope string) map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"active":     true,
		"sub":        subject,
		"username":   testIntrospectUserName,
		"client_id":  clientID,
		"scope":      scope,
		"token_use":  "access_token",
		"token_type": "Bearer",
		"iss":        "https://issuer.example.com",
		"aud":        []string{},
		"exp":        now + 3600,
		"iat":        now,
		"nbf":        now,
		"ext":        map[string]any{"device_label": "Haruki-Client @ home-server", "email": testIntrospectUserEmail},
	}
}

type introspectTestEnv struct {
	t     *testing.T
	hydra *fakeIntrospectHydra
	app   *fiber.App
	logs  *introspectLogBuffer
}

func newIntrospectTestEnv(t *testing.T) *introspectTestEnv {
	t.Helper()
	hydra := &fakeIntrospectHydra{tokens: map[string]map[string]any{}, clients: map[string]map[string]any{}}
	hydra.setClient(testIntrospectClientID, map[string]any{"client_id": testIntrospectClientID, "client_name": "Haruki Client"})
	hydra.setToken(testActiveAccessToken, activeTokenBody(testIntrospectIdentityID, testIntrospectClientID, "user:read offline_access station:room:write"))
	server := httptest.NewServer(http.HandlerFunc(hydra.serve))
	t.Cleanup(server.Close)
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: server.URL, RequestTimeout: 5 * time.Second})

	entClient := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-")))
	t.Cleanup(func() { _ = entClient.Close() })
	ctx := context.Background()
	entClient.User.Create().SetID(testIntrospectUserID).SetName(testIntrospectUserName).SetEmail(testIntrospectUserEmail).SetKratosIdentityID(testIntrospectIdentityID).SaveX(ctx)
	entClient.User.Create().SetID("legacy-user").SetName("Legacy").SetEmail("legacy@example.test").SaveX(ctx)
	entClient.User.Create().SetID("banned-user").SetName("Banned").SetEmail("banned@example.test").SetBanned(true).SaveX(ctx)

	sum := sha256.Sum256([]byte(testInternalToken))
	cfg, err := ParseInternalAPIConfig(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("ParseInternalAPIConfig: %v", err)
	}
	logs := &introspectLogBuffer{}
	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, DBManager: &database.HarukiToolboxDBManager{DB: entClient}}
	RegisterOAuth2InternalRoutes(apiHelper, InternalRouteOptions{
		HydraConfig: hydraConfig,
		Config:      cfg,
		Logger:      harukiLogger.NewLogger("OAuth2InternalTest", "DEBUG", logs),
	})
	return &introspectTestEnv{t: t, hydra: hydra, app: app, logs: logs}
}

type introspectTestResponse struct {
	status int
	header http.Header
	body   []byte
}

func (r introspectTestResponse) json(t *testing.T) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(r.body, &decoded); err != nil {
		t.Fatalf("response %q is not JSON: %v", r.body, err)
	}
	return decoded
}

func (e *introspectTestEnv) post(authorization, contentType, body string) introspectTestResponse {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, InternalIntrospectPath, strings.NewReader(body))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		e.t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		e.t.Fatalf("Cache-Control = %q, want no-store (status %d)", got, resp.StatusCode)
	}
	return introspectTestResponse{status: resp.StatusCode, header: resp.Header, body: raw}
}

func (e *introspectTestEnv) introspect(token string) introspectTestResponse {
	e.t.Helper()
	return e.post("Bearer "+testInternalToken, "application/x-www-form-urlencoded", "token="+token)
}

func (e *introspectTestEnv) assertInactive(resp introspectTestResponse) {
	e.t.Helper()
	if resp.status != http.StatusOK || strings.TrimSpace(string(resp.body)) != `{"active":false}` {
		e.t.Fatalf("status %d body %s, want 200 {\"active\":false}", resp.status, resp.body)
	}
}

func TestInternalIntrospectRequiresToken(t *testing.T) {
	env := newIntrospectTestEnv(t)
	cases := map[string]string{
		"missing":        "",
		"wrong token":    "Bearer wrong-internal-token-value",
		"wrong scheme":   "Basic " + testInternalToken,
		"token as is":    testInternalToken,
		"token prefixed": "Bearer " + testInternalToken + "x",
	}
	for name, authorization := range cases {
		t.Run(name, func(t *testing.T) {
			resp := env.post(authorization, "application/x-www-form-urlencoded", "token="+testActiveAccessToken)
			if resp.status != http.StatusUnauthorized || strings.TrimSpace(string(resp.body)) != `{"error":"unauthorized"}` {
				t.Fatalf("status %d body %s, want 401 {\"error\":\"unauthorized\"}", resp.status, resp.body)
			}
		})
	}
	if got := env.hydra.introspectionCount(); got != 0 {
		t.Fatalf("Hydra introspected %d times for unauthorized calls", got)
	}
	logs := env.logs.String()
	if strings.Count(logs, "oauth2_internal event=unauthorized") != len(cases) || !strings.Contains(logs, "WARN") {
		t.Fatalf("expected one WARN per unauthorized call, logs:\n%s", logs)
	}
	for _, secret := range []string{testInternalToken, "wrong-internal-token-value", testActiveAccessToken} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain a token:\n%s", logs)
		}
	}
}

func TestInternalIntrospectInactiveForDisabledClient(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		env := newIntrospectTestEnv(t)
		env.hydra.setClient(testIntrospectClientID, map[string]any{
			"client_id": testIntrospectClientID,
			"metadata":  map[string]any{"haruki": map[string]any{"active": false}},
		})
		env.assertInactive(env.introspect(testActiveAccessToken))
	})
	t.Run("deleted", func(t *testing.T) {
		env := newIntrospectTestEnv(t)
		env.hydra.setClient(testIntrospectClientID, nil)
		env.assertInactive(env.introspect(testActiveAccessToken))
	})
}

func TestInternalIntrospectInactiveTokens(t *testing.T) {
	env := newIntrospectTestEnv(t)
	refresh := activeTokenBody(testIntrospectIdentityID, testIntrospectClientID, "user:read")
	refresh["token_use"] = "refresh_token"
	env.hydra.setToken("ory_rt_refreshToken0123456789", refresh)
	expired := activeTokenBody(testIntrospectIdentityID, testIntrospectClientID, "user:read")
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	env.hydra.setToken("ory_at_expiredToken0123456789", expired)
	env.hydra.setToken("ory_at_unknownSubject0123456789", activeTokenBody("nobody", testIntrospectClientID, "user:read"))
	env.hydra.setToken("ory_at_bannedSubject0123456789", activeTokenBody("banned-user", testIntrospectClientID, "user:read"))
	env.hydra.setToken("ory_at_noClient0123456789", activeTokenBody(testIntrospectIdentityID, "", "user:read"))

	for _, token := range []string{
		"ory_at_unknownToken0123456789",
		"ory_rt_refreshToken0123456789",
		"ory_at_expiredToken0123456789",
		"ory_at_unknownSubject0123456789",
		"ory_at_bannedSubject0123456789",
		"ory_at_noClient0123456789",
		"not-a-token",
	} {
		t.Run(token, func(t *testing.T) {
			env.assertInactive(env.introspect(token))
		})
	}
}

func TestInternalIntrospectReturnsUserAndScopes(t *testing.T) {
	env := newIntrospectTestEnv(t)
	body := activeTokenBody(testIntrospectIdentityID, testIntrospectClientID, "user:read offline_access station:room:write")
	env.hydra.setToken(testActiveAccessToken, body)

	for name, send := range map[string]func() introspectTestResponse{
		"form": func() introspectTestResponse { return env.introspect(testActiveAccessToken) },
		"json": func() introspectTestResponse {
			return env.post("Bearer "+testInternalToken, "application/json; charset=utf-8", `{"token":"`+testActiveAccessToken+`"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			resp := send()
			if resp.status != http.StatusOK {
				t.Fatalf("status %d body %s", resp.status, resp.body)
			}
			got := resp.json(t)
			want := map[string]any{
				"active":       true,
				"user_id":      testIntrospectUserID,
				"client_id":    testIntrospectClientID,
				"scope":        "user:read offline_access station:room:write",
				"exp":          float64(body["exp"].(int64)),
				"iat":          float64(body["iat"].(int64)),
				"device_label": "Haruki-Client @ home-server",
			}
			if !maps.Equal(got, want) {
				t.Fatalf("response = %v\nwant %v", got, want)
			}
		})
	}

	// A legacy token whose subject is the local users.id still resolves.
	env.hydra.setToken("ory_at_legacySubject0123456789", activeTokenBody("legacy-user", testIntrospectClientID, "user:read"))
	if got := env.introspect("ory_at_legacySubject0123456789").json(t)["user_id"]; got != "legacy-user" {
		t.Fatalf("legacy subject user_id = %v", got)
	}

	// Nothing is cached: a revocation in Hydra applies to the next call.
	before := env.hydra.introspectionCount()
	env.hydra.setToken(testActiveAccessToken, map[string]any{"active": false})
	env.assertInactive(env.introspect(testActiveAccessToken))
	if env.hydra.introspectionCount() != before+1 {
		t.Fatal("the backend answered without asking Hydra")
	}
}

func TestInternalIntrospectNeverLeaksUserFields(t *testing.T) {
	env := newIntrospectTestEnv(t)
	// A browser-flow token: no device label, so the field is omitted.
	browser := activeTokenBody(testIntrospectIdentityID, testIntrospectClientID, "openid user:read")
	browser["ext"] = map[string]any{}
	env.hydra.setToken("ory_at_browserToken0123456789", browser)

	for _, token := range []string{testActiveAccessToken, "ory_at_browserToken0123456789"} {
		resp := env.introspect(token)
		got := resp.json(t)
		allowed := []string{"active", "user_id", "client_id", "scope", "exp", "iat", "device_label"}
		for key := range got {
			if !slices.Contains(allowed, key) {
				t.Fatalf("response carries %q: %s", key, resp.body)
			}
		}
		for _, leak := range []string{testIntrospectUserName, testIntrospectUserEmail, testIntrospectIdentityID, "issuer.example.com", "token_use", "username", "sub"} {
			if strings.Contains(string(resp.body), leak) {
				t.Fatalf("response leaks %q: %s", leak, resp.body)
			}
		}
	}
	if _, ok := env.introspect("ory_at_browserToken0123456789").json(t)["device_label"]; ok {
		t.Fatal("device_label must be omitted when the token has none")
	}
}

func TestInternalIntrospectRejectsMalformedRequests(t *testing.T) {
	env := newIntrospectTestEnv(t)
	auth := "Bearer " + testInternalToken
	cases := []struct{ name, contentType, body string }{
		{"no content type", "", "token=" + testActiveAccessToken},
		{"text", "text/plain", "token=" + testActiveAccessToken},
		{"empty form", "application/x-www-form-urlencoded", ""},
		{"blank token", "application/x-www-form-urlencoded", "token=%20"},
		{"two tokens", "application/x-www-form-urlencoded", "token=a&token=b"},
		{"json number", "application/json", `{"token":1}`},
		{"json broken", "application/json", `{"token":`},
		{"oversized", "application/x-www-form-urlencoded", "token=" + strings.Repeat("a", internalIntrospectMaxTokenLength+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.post(auth, tc.contentType, tc.body)
			if resp.status != http.StatusBadRequest || strings.TrimSpace(string(resp.body)) != `{"error":"invalid_request"}` {
				t.Fatalf("status %d body %s, want 400 invalid_request", resp.status, resp.body)
			}
		})
	}
	if got := env.hydra.introspectionCount(); got != 0 {
		t.Fatalf("Hydra introspected %d malformed requests", got)
	}
}

func TestInternalIntrospectHydraFailureIsUnavailable(t *testing.T) {
	env := newIntrospectTestEnv(t)
	env.hydra.mu.Lock()
	env.hydra.introspectFail = true
	env.hydra.mu.Unlock()
	resp := env.introspect(testActiveAccessToken)
	if resp.status != http.StatusServiceUnavailable || strings.TrimSpace(string(resp.body)) != `{"error":"temporarily_unavailable"}` {
		t.Fatalf("status %d body %s, want 503 temporarily_unavailable", resp.status, resp.body)
	}
	if logs := env.logs.String(); strings.Contains(logs, testActiveAccessToken) || !strings.Contains(logs, "event=introspect_failed") {
		t.Fatalf("logs:\n%s", logs)
	}
}

func TestParseInternalAPIConfig(t *testing.T) {
	sum := sha256.Sum256([]byte(testInternalToken))
	lower := hex.EncodeToString(sum[:])
	for _, value := range []string{lower, strings.ToUpper(lower), "  " + lower + "\n"} {
		cfg, err := ParseInternalAPIConfig(value)
		if err != nil || !cfg.Enabled() {
			t.Fatalf("ParseInternalAPIConfig(%q) = %v, %v", value, cfg.Enabled(), err)
		}
		if cfg.authorize("Bearer "+testInternalToken) != "" {
			t.Fatalf("token does not match its own hash %q", value)
		}
	}
	for _, value := range []string{"", "   "} {
		cfg, err := ParseInternalAPIConfig(value)
		if err != nil || cfg.Enabled() {
			t.Fatalf("ParseInternalAPIConfig(%q) = %v, %v; want disabled", value, cfg.Enabled(), err)
		}
	}
	for _, value := range []string{lower[:63], lower + "0", "g" + lower[1:], testInternalToken} {
		if _, err := ParseInternalAPIConfig(value); err == nil {
			t.Fatalf("ParseInternalAPIConfig(%q) accepted", value)
		}
	}
	// The zero value never authorizes, not even an empty-string token.
	if (InternalAPIConfig{}).authorize("Bearer x") == "" {
		t.Fatal("zero config authorized a token")
	}
}

func TestRegisterOAuth2InternalRoutesSkipsWithoutHash(t *testing.T) {
	app := fiber.New()
	RegisterOAuth2InternalRoutes(&harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app}, InternalRouteOptions{})
	if routes := app.GetRoutes(true); len(routes) != 0 {
		t.Fatalf("routes registered without a token hash: %v", routes)
	}
}
