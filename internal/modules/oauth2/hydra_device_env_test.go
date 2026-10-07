package oauth2

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	json "encoding/json/v2"
	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

const (
	testPublicClientID       = "haruki-client"
	testConfidentialClientID = "station-cli"
	testConfidentialSecret   = "s3cret+value"
	testDeviceScope          = "offline_access user:read"
	testFrontendURL          = "https://haruki.example.com"
	testUserCodeCharset      = "BCDFGHJKLMNPQRSTVWXZ"
	testHydraDeviceTTL       = 599
	testSessionSignToken     = "device-flow-test-session-sign-token"
)

var testDeviceEpoch = time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)

// testDeviceFlowOptions mirrors the startup defaults of oauth2.device_flow.
func testDeviceFlowOptions(logger *harukiLogger.Logger) DeviceFlowConfigOptions {
	return DeviceFlowConfigOptions{
		Enabled:         true,
		FrontendURL:     testFrontendURL,
		HydraIssuerURL:  "https://api.example.com",
		UserCodeCharset: testUserCodeCharset,
		UserCodeLength:  8,
		UserCodeTTL:     10 * time.Minute,
		Timings: DeviceFlowTimings{
			MinPollInterval:       5 * time.Second,
			ClaimTTL:              300 * time.Second,
			ApproveLease:          30 * time.Second,
			ApprovalTimeout:       15 * time.Second,
			MinRemainingToApprove: 30 * time.Second,
			MaxApproveAttempts:    3,
			RecordGrace:           1800 * time.Second,
			ReaperInterval:        60 * time.Second,
			ReaperGrace:           60 * time.Second,
		},
		Limits: DeviceFlowLimits{
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

// syncBuffer is a log sink safe to read while handlers write to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fakeDeviceHydraCall struct {
	Method   string
	Path     string
	RawQuery string
	Body     string
	Header   http.Header
}

// fakeDeviceHydra serves the Hydra public and admin endpoints the device flow
// uses, on one server, and records every call.
type fakeDeviceHydra struct {
	mu      sync.Mutex
	clients map[string]map[string]any
	// clientLookupStatus, when set, answers every client lookup with it.
	clientLookupStatus int
	// deviceAuth answers POST /oauth2/device/auth; nil issues a new code.
	deviceAuth func(form url.Values) (int, any)
	// token answers POST /oauth2/token; nil answers authorization_pending.
	token        func(form url.Values, header http.Header) (int, any)
	revokeStatus int
	// chain, when set, serves the browser leg the approval chain walks
	// (verify, device/login/consent requests and accepts).
	chain       *fakeDeviceChain
	calls       []fakeDeviceHydraCall
	issued      int
	userCodes   []string
	deviceCodes []string
}

func (f *fakeDeviceHydra) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, fakeDeviceHydraCall{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Body: string(raw), Header: r.Header.Clone()})
	f.mu.Unlock()
	form, _ := url.ParseQuery(string(raw))
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/admin/clients/"):
		f.mu.Lock()
		status := f.clientLookupStatus
		client, ok := f.clients[strings.TrimPrefix(r.URL.Path, "/admin/clients/")]
		f.mu.Unlock()
		if status != 0 {
			writeFakeHydraJSON(w, status, map[string]any{"error": "server_error"})
			return
		}
		if !ok {
			writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "Unable to locate the resource"})
			return
		}
		writeFakeHydraJSON(w, http.StatusOK, client)
	case r.Method == http.MethodPost && r.URL.Path == "/oauth2/device/auth":
		f.mu.Lock()
		handler := f.deviceAuth
		f.mu.Unlock()
		if handler != nil {
			status, body := handler(form)
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Basic realm="hydra"`)
			}
			writeFakeHydraJSON(w, status, body)
			return
		}
		writeFakeHydraJSON(w, http.StatusOK, f.issueCode())
	case r.Method == http.MethodPost && r.URL.Path == "/oauth2/token":
		f.mu.Lock()
		handler := f.token
		f.mu.Unlock()
		if handler == nil {
			writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "authorization_pending", "error_description": "The authorization request is still pending."})
			return
		}
		status, body := handler(form, r.Header)
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="hydra"`)
		}
		writeFakeHydraJSON(w, status, body)
	case r.Method == http.MethodDelete && r.URL.Path == "/admin/oauth2/auth/sessions/consent":
		f.mu.Lock()
		status := f.revokeStatus
		f.mu.Unlock()
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	default:
		f.mu.Lock()
		chain := f.chain
		f.mu.Unlock()
		if chain != nil && chain.serve(w, r, raw) {
			return
		}
		writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
}

// issueCode returns a fresh Hydra device/auth answer, including the stray
// "Header" member Hydra v25.4.0 serializes.
func (f *fakeDeviceHydra) issueCode() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issued++
	userCode := testUserCodeFromIndex(f.issued)
	deviceCode := fmt.Sprintf("ory_dc_%040d.signature", f.issued)
	f.userCodes = append(f.userCodes, userCode)
	f.deviceCodes = append(f.deviceCodes, deviceCode)
	return map[string]any{
		"Header":                    nil,
		"device_code":               deviceCode,
		"user_code":                 userCode,
		"verification_uri":          "https://api.example.com/oauth2/device/verify",
		"verification_uri_complete": "https://api.example.com/oauth2/device/verify?user_code=" + userCode,
		"expires_in":                testHydraDeviceTTL,
		"interval":                  5,
	}
}

func testUserCodeFromIndex(index int) string {
	var b strings.Builder
	for range 8 {
		b.WriteByte(testUserCodeCharset[index%len(testUserCodeCharset)])
		index /= len(testUserCodeCharset)
	}
	return b.String()
}

func (f *fakeDeviceHydra) callsTo(method, path string) []fakeDeviceHydraCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []fakeDeviceHydraCall
	for _, call := range f.calls {
		if call.Method == method && call.Path == path {
			matched = append(matched, call)
		}
	}
	return matched
}

func (f *fakeDeviceHydra) setClient(client map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients[client["client_id"].(string)] = client
}

func testDeviceClient(clientID, authMethod, scope string, grantTypes []string, metadata map[string]any) map[string]any {
	client := map[string]any{
		"client_id":                  clientID,
		"client_name":                "Client " + clientID,
		"token_endpoint_auth_method": authMethod,
		"grant_types":                grantTypes,
		"scope":                      scope,
	}
	if metadata != nil {
		client["metadata"] = metadata
	}
	return client
}

var testDeviceGrantTypes = []string{HydraGrantTypeDeviceCode, HydraGrantTypeRefreshToken}

type deviceTestEnv struct {
	t           *testing.T
	redis       *miniredis.Miniredis
	db          *database.HarukiToolboxDBManager
	apiHelper   *harukiAPIHelper.HarukiToolboxRouterHelpers
	hydra       *fakeDeviceHydra
	hydraConfig *harukiOAuth2.HydraConfig
	store       *deviceFlowStore
	cfg         DeviceFlowConfig
	logs        *syncBuffer
	logger      *harukiLogger.Logger
	app         *fiber.App
	clock       atomic.Int64
	gateOn      atomic.Bool
	gateErr     atomic.Bool

	mu         sync.Mutex
	authBodies []string
	tokenBody  []string
	issuedHDC  []string
	// browser holds the lookup/approve/deny responses; flowHandles every
	// handle a lookup issued.
	browser     []deviceBrowserRecord
	flowHandles []string
}

// deviceBrowserRecord is one browser-endpoint response. Only a successful
// lookup may carry a flow handle (the one it issues) and the formatted user
// code (the one the user typed).
type deviceBrowserRecord struct {
	Path   string
	Status int
	Body   string
}

type deviceTestEnvOption func(*DeviceFlowConfigOptions)

func newDeviceTestEnv(t *testing.T, options ...deviceTestEnvOption) *deviceTestEnv {
	t.Helper()
	env := &deviceTestEnv{t: t, logs: &syncBuffer{}}
	env.clock.Store(testDeviceEpoch.UnixMilli())
	env.gateOn.Store(true)

	env.redis = miniredis.RunT(t)
	port, err := strconv.Atoi(env.redis.Port())
	if err != nil {
		t.Fatal(err)
	}
	manager := harukiRedis.NewRedisClient(harukiConfig.RedisConfig{Host: env.redis.Host(), Port: port}, testSessionSignToken)
	t.Cleanup(func() { _ = manager.Close() })
	entClient := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-")))
	t.Cleanup(func() { _ = entClient.Close() })
	env.db = &database.HarukiToolboxDBManager{DB: entClient, Redis: manager}
	env.apiHelper = &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: env.db}

	env.hydra = &fakeDeviceHydra{clients: map[string]map[string]any{}}
	env.hydra.setClient(testDeviceClient(testPublicClientID, "none", "openid profile offline_access user:read bindings:read game-data:read game-data:write email", testDeviceGrantTypes, nil))
	env.hydra.setClient(testDeviceClient(testConfidentialClientID, "client_secret_basic", "openid offline_access user:read game-data:read game-data:write", testDeviceGrantTypes, nil))
	server := httptest.NewServer(http.HandlerFunc(env.hydra.serve))
	t.Cleanup(server.Close)
	env.hydraConfig = harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		PublicURL: server.URL, BrowserURL: server.URL, AdminURL: server.URL, RequestTimeout: 5 * time.Second,
	})

	env.logger = harukiLogger.NewLogger("OAuth2DeviceTest", "DEBUG", env.logs)
	opts := testDeviceFlowOptions(env.logger)
	for _, option := range options {
		option(&opts)
	}
	env.cfg = NewDeviceFlowConfig(opts).WithRuntimeGate(func(context.Context) (bool, error) {
		if env.gateErr.Load() {
			return false, errors.New("runtime config unavailable")
		}
		return env.gateOn.Load(), nil
	})
	// Defeat the 1 s gate cache: every read sees the switch as just set.
	var gateTick atomic.Int64
	env.cfg.gate.now = func() time.Time { return time.Unix(gateTick.Add(10), 0) }
	env.store = newDeviceFlowStore(env.db)
	env.store.now = env.now
	env.app = fiber.New()
	env.app.Post("/api/oauth2/device/auth", handleHydraDeviceAuthorization(env.hydraConfig, env.cfg, env.store))
	env.app.Post("/api/oauth2/token", handleHydraTokenEndpoint(env.apiHelper, env.hydraConfig, env.cfg, env.store))
	return env
}

func (e *deviceTestEnv) now() time.Time { return time.UnixMilli(e.clock.Load()).UTC() }

func (e *deviceTestEnv) advance(d time.Duration) { e.clock.Add(d.Milliseconds()) }

type deviceTestResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r deviceTestResponse) json(t *testing.T) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(r.Body, &decoded); err != nil {
		t.Fatalf("response body %q is not JSON: %v", r.Body, err)
	}
	return decoded
}

func (r deviceTestResponse) oauthError(t *testing.T) string {
	t.Helper()
	code, _ := r.json(t)["error"].(string)
	return code
}

func (e *deviceTestEnv) do(path, contentType, body string, header map[string]string) deviceTestResponse {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range header {
		req.Header.Set(name, value)
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		e.t.Fatalf("app.Test(%s) returned error: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	e.mu.Lock()
	if strings.HasPrefix(path, "/api/oauth2/device/") && path != "/api/oauth2/device/auth" {
		e.browser = append(e.browser, deviceBrowserRecord{Path: path, Status: resp.StatusCode, Body: string(raw)})
	} else if path == "/api/oauth2/token" {
		e.tokenBody = append(e.tokenBody, string(raw))
	} else {
		e.authBodies = append(e.authBodies, string(raw))
	}
	e.mu.Unlock()
	return deviceTestResponse{Status: resp.StatusCode, Header: resp.Header, Body: raw}
}

func basicAuth(clientID, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(clientID)+":"+url.QueryEscape(secret)))
}

func (e *deviceTestEnv) deviceAuth(form url.Values, authorization string) deviceTestResponse {
	e.t.Helper()
	header := map[string]string{}
	if authorization != "" {
		header["Authorization"] = authorization
	}
	return e.do("/api/oauth2/device/auth", formURLEncodedMediaType, form.Encode(), header)
}

// issue runs a successful device/auth for a client and returns the wrapped
// device code and the flow ID behind it.
func (e *deviceTestEnv) issue(clientID string) (string, string) {
	e.t.Helper()
	form := url.Values{"scope": {testDeviceScope}}
	authorization := ""
	if clientID == testConfidentialClientID {
		authorization = basicAuth(clientID, testConfidentialSecret)
	} else {
		form.Set("client_id", clientID)
	}
	resp := e.deviceAuth(form, authorization)
	if resp.Status != http.StatusOK {
		e.t.Fatalf("device/auth status = %d, body %s", resp.Status, resp.Body)
	}
	hdc, _ := resp.json(e.t)["device_code"].(string)
	flowID, found, err := e.store.flowIDForDeviceCode(context.Background(), hdc)
	if err != nil || !found {
		e.t.Fatalf("flow for issued code not found: found=%v err=%v", found, err)
	}
	e.mu.Lock()
	e.issuedHDC = append(e.issuedHDC, hdc)
	e.mu.Unlock()
	return hdc, flowID
}

func (e *deviceTestEnv) poll(hdc, clientID string) deviceTestResponse {
	e.t.Helper()
	form := url.Values{"grant_type": {HydraGrantTypeDeviceCode}, "device_code": {hdc}}
	header := map[string]string{}
	if clientID == testConfidentialClientID {
		header["Authorization"] = basicAuth(clientID, testConfidentialSecret)
	} else {
		form.Set("client_id", clientID)
	}
	return e.do("/api/oauth2/token", formURLEncodedMediaType, form.Encode(), header)
}

func (e *deviceTestEnv) flowKey(flowID string) string {
	return e.db.Redis.KeyBuilder().BuildOAuth2DeviceFlowKey(flowID)
}

func (e *deviceTestEnv) unredeemedKey() string {
	return e.db.Redis.KeyBuilder().BuildOAuth2DeviceUnredeemedKey()
}

func (e *deviceTestEnv) field(flowID, name string) string {
	return e.redis.HGet(e.flowKey(flowID), name)
}

// setFlow overwrites flow fields, e.g. to put a flow in a state only the
// browser endpoints (BE-7) reach.
func (e *deviceTestEnv) setFlow(flowID string, fieldValues ...string) {
	e.t.Helper()
	e.redis.HSet(e.flowKey(flowID), fieldValues...)
}

// approve puts a flow in an approved-class state with a recorded consent
// request, as the server-driven chain would.
func (e *deviceTestEnv) approve(flowID, state, consentRequestID string) {
	e.t.Helper()
	e.setFlow(flowID, "st", state, "cby", "4242", "crid", consentRequestID, "sub", "kratos-4242")
	exp, _ := strconv.ParseFloat(e.field(flowID, "exp"), 64)
	if _, err := e.redis.ZAdd(e.unredeemedKey(), exp, flowID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *deviceTestEnv) inUnredeemed(flowID string) bool {
	members, _ := e.redis.ZMembers(e.unredeemedKey())
	return slices.Contains(members, flowID)
}

// assertNoSecretLeaks checks that no user code, wrapped or Hydra device code
// or flow handle appears in the logs, in any Redis key name or stored value,
// in token-endpoint responses, or (for Hydra's code) in any response at all.
// Browser responses carry none of them either, except that a successful
// lookup returns the handle it issued and the code the user typed. Nor do
// the approval chain's challenges, verifiers and cookies reach logs or
// browser responses.
func (e *deviceTestEnv) assertNoSecretLeaks() {
	e.t.Helper()
	e.assertNoDeviceCodeLeaks()
	e.assertNoBrowserLeaks()
}

func (e *deviceTestEnv) assertNoDeviceCodeLeaks() {
	e.t.Helper()
	e.hydra.mu.Lock()
	userCodes := append([]string(nil), e.hydra.userCodes...)
	deviceCodes := append([]string(nil), e.hydra.deviceCodes...)
	e.hydra.mu.Unlock()
	e.mu.Lock()
	hdcs := append([]string(nil), e.issuedHDC...)
	authBodies := strings.Join(e.authBodies, "\n")
	tokenBodies := strings.Join(e.tokenBody, "\n")
	e.mu.Unlock()
	for _, body := range e.authBodies {
		var decoded map[string]any
		if json.Unmarshal([]byte(body), &decoded) == nil {
			if hdc, ok := decoded["device_code"].(string); ok {
				hdcs = append(hdcs, hdc)
			}
		}
	}

	var redisDump strings.Builder
	for _, key := range e.redis.Keys() {
		redisDump.WriteString(key + "\n")
	}
	keyNames := redisDump.String()
	for _, key := range e.redis.Keys() {
		switch e.redis.Type(key) {
		case "hash":
			names, _ := e.redis.HKeys(key)
			for _, name := range names {
				redisDump.WriteString(name + "=" + e.redis.HGet(key, name) + "\n")
			}
		case "string":
			value, _ := e.redis.Get(key)
			redisDump.WriteString(value + "\n")
		case "zset":
			members, _ := e.redis.ZMembers(key)
			redisDump.WriteString(strings.Join(members, ",") + "\n")
		}
	}
	logs := e.logs.String()

	secrets := map[string][]string{}
	for _, code := range userCodes {
		secrets["user code"] = append(secrets["user code"], code, formatDeviceUserCode(code))
	}
	secrets["wrapped device code"] = hdcs
	secrets["Hydra device code"] = deviceCodes
	for kind, values := range secrets {
		for _, value := range values {
			if strings.Contains(logs, value) {
				e.t.Errorf("logs contain a %s", kind)
			}
			if strings.Contains(keyNames, value) {
				e.t.Errorf("a Redis key name contains a %s", kind)
			}
			if strings.Contains(redisDump.String(), value) {
				e.t.Errorf("Redis stores a %s in clear", kind)
			}
			if strings.Contains(tokenBodies, value) {
				e.t.Errorf("a token endpoint response contains a %s", kind)
			}
		}
	}
	for _, code := range deviceCodes {
		if strings.Contains(authBodies, code) || strings.Contains(authBodies, "ory_dc_") || strings.Contains(tokenBodies, "ory_dc_") {
			e.t.Error("a response contains Hydra's device code")
		}
	}
}

func assertNoStore(t *testing.T, resp deviceTestResponse) {
	t.Helper()
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Pragma") != "no-cache" {
		t.Fatalf("Cache-Control = %q, Pragma = %q; want no-store, no-cache", resp.Header.Get("Cache-Control"), resp.Header.Get("Pragma"))
	}
}
