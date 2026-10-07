package oauth2

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	json "encoding/json/v2"
	"github.com/gofiber/fiber/v3"
)

const (
	testIssuerURL          = "https://api.example.com"
	testUserID             = "4242"
	testUserName           = "Alice"
	testOtherUserID        = "5151"
	testOtherUserName      = "Bob"
	testUserSession        = "proxy-session-alice"
	testHydraErrorDetail   = "hydra-internal-detail-must-not-leak"
	testUserIDHeader       = "X-Test-User"
	testUserSessionHeader  = "X-Test-Session"
	testCSRFCookieDevice   = "ory_hydra_device_csrf"
	testCSRFCookieLogin    = "ory_hydra_login_csrf_dev_1"
	testCSRFCookieConsent  = "ory_hydra_consent_csrf_dev_1"
	chainStageVerifyStart  = "H9b"
	chainStageDeviceAccept = "H9c"
	chainStageVerifyDevice = "H9d"
	chainStageLoginGet     = "H9e"
	chainStageLoginAccept  = "H9f"
	chainStageVerifyLogin  = "H9g"
	chainStageConsentGet   = "H9h"
	chainStageConsentAcc   = "H9i"
	chainStageVerifyFinal  = "H9j"
)

// fakeDeviceChain plays Hydra's browser leg of a device flow the way
// Hydra v25.4.0 does in non-dev mode (design §3.1): Secure cookies that must
// come back on the next hop or Hydra answers 403, the flow marker and
// client_id carried through every redirect_to and request_url, and the final
// consent verifier usable once.
type fakeDeviceChain struct {
	t        *testing.T
	hydra    *fakeDeviceHydra
	clientID string
	scopes   []string

	mu   sync.Mutex
	seq  int
	hits map[string]int
	// hook may answer a stage itself (return true); hit counts from 1.
	hook func(stage string, hit int, w http.ResponseWriter, r *http.Request) bool
	// mutateJSON edits an admin JSON answer before it is sent.
	mutateJSON func(stage string, body map[string]any)
	// mutateLocation edits a public 302 Location before it is sent.
	mutateLocation func(stage string, location string) string

	deviceChallenges  map[string]string // device_challenge → H9b query
	loginRequestURL   map[string]string // login_challenge → request_url
	loginVerifiers    map[string]string // login_verifier → request_url
	consentRequestURL map[string]string // consent_challenge → request_url
	consentVerifiers  map[string]bool   // consent_verifier → used
	csrf              map[string]string // cookie name → expected value
	loginSubject      string
	loginAcceptBody   map[string]any
	consentAcceptBody map[string]any
	consentRequestIDs []string
	completed         int
	// secrets are every challenge, verifier and cookie value handed out.
	secrets []string
}

func newFakeDeviceChain(t *testing.T, hydra *fakeDeviceHydra) *fakeDeviceChain {
	chain := &fakeDeviceChain{
		t:                 t,
		hydra:             hydra,
		clientID:          testPublicClientID,
		scopes:            normalizeDeviceScope(testDeviceScope),
		hits:              map[string]int{},
		deviceChallenges:  map[string]string{},
		loginRequestURL:   map[string]string{},
		loginVerifiers:    map[string]string{},
		consentRequestURL: map[string]string{},
		consentVerifiers:  map[string]bool{},
		csrf:              map[string]string{},
	}
	hydra.mu.Lock()
	hydra.chain = chain
	hydra.mu.Unlock()
	return chain
}

func (f *fakeDeviceChain) next(prefix string) string {
	f.seq++
	value := fmt.Sprintf("%s-%04d-secret", prefix, f.seq)
	f.secrets = append(f.secrets, value)
	return value
}

func chainStageOf(r *http.Request) string {
	query := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == hydraDeviceVerifyPath:
		switch {
		case query.Has("consent_verifier"):
			return chainStageVerifyFinal
		case query.Has("login_verifier"):
			return chainStageVerifyLogin
		case query.Has("device_verifier"):
			return chainStageVerifyDevice
		}
		return chainStageVerifyStart
	case r.Method == http.MethodPut && r.URL.Path == hydraDeviceAcceptEndpoint:
		return chainStageDeviceAccept
	case r.Method == http.MethodGet && r.URL.Path == "/admin/oauth2/auth/requests/login":
		return chainStageLoginGet
	case r.Method == http.MethodPut && r.URL.Path == hydraLoginAcceptEndpoint:
		return chainStageLoginAccept
	case r.Method == http.MethodGet && r.URL.Path == "/admin/oauth2/auth/requests/consent":
		return chainStageConsentGet
	case r.Method == http.MethodPut && r.URL.Path == hydraConsentAcceptEndpoint:
		return chainStageConsentAcc
	}
	return ""
}

func (f *fakeDeviceChain) stageHits(stage string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[stage]
}

func (f *fakeDeviceChain) serve(w http.ResponseWriter, r *http.Request, raw []byte) bool {
	stage := chainStageOf(r)
	if stage == "" {
		return false
	}
	f.mu.Lock()
	f.hits[stage]++
	hit := f.hits[stage]
	hook := f.hook
	f.mu.Unlock()
	if hook != nil && hook(stage, hit, w, r) {
		return true
	}
	var body map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	query := r.URL.Query()
	issuerVerify := testIssuerURL + hydraDeviceVerifyPath
	switch stage {
	case chainStageVerifyStart:
		challenge := f.next("device-challenge")
		f.deviceChallenges[challenge] = r.URL.RawQuery
		f.setCookie(w, testCSRFCookieDevice)
		f.redirect(w, stage, testFrontendURL+"/device?device_challenge="+url.QueryEscape(challenge))
	case chainStageDeviceAccept:
		startQuery, ok := f.deviceChallenges[query.Get("device_challenge")]
		if !ok {
			f.adminError(w, http.StatusNotFound)
			return true
		}
		userCode, _ := body["user_code"].(string)
		f.hydra.mu.Lock()
		known := slices.Contains(f.hydra.userCodes, userCode)
		f.hydra.mu.Unlock()
		if !known || f.completed > 0 {
			f.adminError(w, http.StatusBadRequest)
			return true
		}
		start, _ := url.ParseQuery(startQuery)
		redirect := url.Values{"client_id": {f.clientID}, "device_verifier": {f.next("device-verifier")}}
		for name, values := range start {
			redirect[name] = values
		}
		f.adminJSON(w, stage, map[string]any{"redirect_to": issuerVerify + "?" + redirect.Encode()})
	case chainStageVerifyDevice:
		if !f.hasCookie(r, testCSRFCookieDevice) {
			writeFakeHydraJSON(w, http.StatusForbidden, map[string]any{"error": "No CSRF value available in the session cookie"})
			return true
		}
		challenge := f.next("login-challenge")
		f.loginRequestURL[challenge] = issuerVerify + "?" + r.URL.RawQuery
		f.setCookie(w, testCSRFCookieLogin)
		f.redirect(w, stage, testFrontendURL+"/oauth2/login?login_challenge="+url.QueryEscape(challenge))
	case chainStageLoginGet:
		requestURL, ok := f.loginRequestURL[query.Get("login_challenge")]
		if !ok {
			f.adminError(w, http.StatusNotFound)
			return true
		}
		f.adminJSON(w, stage, map[string]any{
			"challenge": query.Get("login_challenge"), "skip": false, "subject": "",
			"request_url": requestURL, "requested_scope": slices.Clone(f.scopes),
			"requested_access_token_audience": []string{}, "client": map[string]any{"client_id": f.clientID},
		})
	case chainStageLoginAccept:
		requestURL, ok := f.loginRequestURL[query.Get("login_challenge")]
		if !ok {
			f.adminError(w, http.StatusNotFound)
			return true
		}
		f.loginAcceptBody = body
		f.loginSubject, _ = body["subject"].(string)
		verifier := f.next("login-verifier")
		f.loginVerifiers[verifier] = requestURL
		f.adminJSON(w, stage, map[string]any{"redirect_to": requestURL + "&login_verifier=" + url.QueryEscape(verifier)})
	case chainStageVerifyLogin:
		requestURL, ok := f.loginVerifiers[query.Get("login_verifier")]
		if !ok || !f.hasCookie(r, testCSRFCookieLogin) {
			writeFakeHydraJSON(w, http.StatusForbidden, map[string]any{"error": "No CSRF value available in the session cookie"})
			return true
		}
		challenge := f.next("consent-challenge")
		f.consentRequestURL[challenge] = requestURL
		f.setCookie(w, testCSRFCookieConsent)
		// Hydra expires the login CSRF cookie once used.
		http.SetCookie(w, &http.Cookie{Name: testCSRFCookieLogin, Value: "", MaxAge: -1, Path: "/", Secure: true})
		f.redirect(w, stage, testFrontendURL+"/oauth2/consent?consent_challenge="+url.QueryEscape(challenge))
	case chainStageConsentGet:
		requestURL, ok := f.consentRequestURL[query.Get("consent_challenge")]
		if !ok {
			f.adminError(w, http.StatusNotFound)
			return true
		}
		consentRequestID := fmt.Sprintf("consent-request-%d", len(f.consentRequestIDs)+1)
		f.consentRequestIDs = append(f.consentRequestIDs, consentRequestID)
		f.adminJSON(w, stage, map[string]any{
			"challenge": query.Get("consent_challenge"), "consent_request_id": consentRequestID,
			"skip": false, "subject": f.loginSubject, "request_url": requestURL,
			"requested_scope": slices.Clone(f.scopes), "requested_access_token_audience": []string{},
			"client": map[string]any{"client_id": f.clientID},
		})
	case chainStageConsentAcc:
		requestURL, ok := f.consentRequestURL[query.Get("consent_challenge")]
		if !ok {
			f.adminError(w, http.StatusNotFound)
			return true
		}
		f.consentAcceptBody = body
		verifier := f.next("consent-verifier")
		f.consentVerifiers[verifier] = false
		f.adminJSON(w, stage, map[string]any{"redirect_to": requestURL + "&consent_verifier=" + url.QueryEscape(verifier)})
	case chainStageVerifyFinal:
		used, ok := f.consentVerifiers[query.Get("consent_verifier")]
		if !ok || used || !f.hasCookie(r, testCSRFCookieConsent) {
			writeFakeHydraJSON(w, http.StatusForbidden, map[string]any{"error": "The consent verifier has already been used"})
			return true
		}
		f.consentVerifiers[query.Get("consent_verifier")] = true
		f.completed++
		f.redirect(w, stage, testFrontendURL+"/device/done?client_id="+url.QueryEscape(f.clientID))
	}
	return true
}

func (f *fakeDeviceChain) setCookie(w http.ResponseWriter, name string) {
	value := f.next("cookie")
	f.csrf[name] = value
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (f *fakeDeviceChain) hasCookie(r *http.Request, name string) bool {
	cookie, err := r.Cookie(name)
	return err == nil && cookie.Value == f.csrf[name] && cookie.Value != ""
}

func (f *fakeDeviceChain) redirect(w http.ResponseWriter, stage, location string) {
	if f.mutateLocation != nil {
		location = f.mutateLocation(stage, location)
	}
	w.Header().Del("Content-Type")
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

func (f *fakeDeviceChain) adminJSON(w http.ResponseWriter, stage string, body map[string]any) {
	if f.mutateJSON != nil {
		f.mutateJSON(stage, body)
	}
	writeFakeHydraJSON(w, http.StatusOK, body)
}

func (f *fakeDeviceChain) adminError(w http.ResponseWriter, status int) {
	writeFakeHydraJSON(w, status, map[string]any{"error": "request_error", "error_description": testHydraErrorDetail})
}

func (f *fakeDeviceChain) allSecrets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.secrets)
}

// closeConnection makes the backend see a transport error.
func closeConnection(t *testing.T, w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("response writer cannot be hijacked")
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

// deviceBrowserEnv is a device test env with the browser endpoints mounted
// behind a session stub, two users, and the chain fake.
type deviceBrowserEnv struct {
	*deviceTestEnv
	chain    *fakeDeviceChain
	handlers *deviceBrowserHandlers
}

func newDeviceBrowserEnv(t *testing.T, options ...deviceTestEnvOption) *deviceBrowserEnv {
	t.Helper()
	env := &deviceBrowserEnv{deviceTestEnv: newDeviceTestEnv(t, options...)}
	env.chain = newFakeDeviceChain(t, env.hydra)
	for id, name := range map[string]string{testUserID: testUserName, testOtherUserID: testOtherUserName} {
		env.db.DB.User.Create().SetID(id).SetName(name).SetEmail(id + "@example.com").SetKratosIdentityID("kratos-" + id).SaveX(t.Context())
	}
	env.handlers = newDeviceBrowserHandlers(env.apiHelper, env.hydraConfig, env.cfg, env.store)
	// Stands in for the session guard: it sets what VerifySessionToken would.
	session := func(c fiber.Ctx) error {
		if userID := c.Get(testUserIDHeader); userID != "" {
			c.Locals("userID", userID)
			c.Locals("identityID", "kratos-"+userID)
			c.Locals("emailVerified", true)
			if sessionID := c.Get(testUserSessionHeader); sessionID != "" {
				c.Locals("authProxySessionID", sessionID)
			}
		}
		return c.Next()
	}
	env.app.Post("/api/oauth2/device/lookup", session, env.handlers.handleDeviceLookup)
	env.app.Post("/api/oauth2/device/approve", session, env.handlers.handleDeviceApprove)
	env.app.Post("/api/oauth2/device/deny", session, env.handlers.handleDeviceDeny)
	return env
}

// browserCall describes a browser request; zero fields take the defaults
// (JSON, the frontend Origin, Alice in her proxy session).
type browserCall struct {
	userID      string
	session     string
	contentType string
	origin      string
	noOrigin    bool
}

func (e *deviceBrowserEnv) post(path string, body any, call browserCall) deviceTestResponse {
	e.t.Helper()
	raw, ok := body.(string)
	if !ok {
		encoded, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		raw = string(encoded)
	}
	header := map[string]string{}
	if call.userID == "" {
		call.userID = testUserID
	}
	if call.session == "" {
		call.session = testUserSession
	}
	header[testUserIDHeader] = call.userID
	header[testUserSessionHeader] = call.session
	if !call.noOrigin {
		if call.origin == "" {
			call.origin = testFrontendURL
		}
		header["Origin"] = call.origin
	}
	if call.contentType == "" {
		call.contentType = "application/json"
	}
	return e.do(path, call.contentType, raw, header)
}

func (e *deviceBrowserEnv) lookup(userCode string, call browserCall) deviceTestResponse {
	e.t.Helper()
	return e.post("/api/oauth2/device/lookup", map[string]any{"userCode": userCode}, call)
}

// claim issues a flow for the public client and looks it up as Alice,
// returning the flow ID, the user code Hydra issued and the flow handle.
func (e *deviceBrowserEnv) claim() (string, string, string) {
	e.t.Helper()
	_, flowID := e.issue(testPublicClientID)
	userCode := e.lastUserCode()
	resp := e.lookup(formatDeviceUserCode(userCode), browserCall{})
	if resp.Status != http.StatusOK {
		e.t.Fatalf("lookup status = %d, body %s", resp.Status, resp.Body)
	}
	handle := updatedData(e.t, resp)["flowHandle"].(string)
	e.mu.Lock()
	e.flowHandles = append(e.flowHandles, handle)
	e.mu.Unlock()
	return flowID, userCode, handle
}

func (e *deviceBrowserEnv) lastUserCode() string {
	e.hydra.mu.Lock()
	defer e.hydra.mu.Unlock()
	return e.hydra.userCodes[len(e.hydra.userCodes)-1]
}

func (e *deviceBrowserEnv) approve(handle, userCode string, call browserCall) deviceTestResponse {
	e.t.Helper()
	return e.post("/api/oauth2/device/approve", map[string]any{"flowHandle": handle, "userCode": userCode, "acknowledged": true}, call)
}

func (e *deviceBrowserEnv) deny(handle, reason string, call browserCall) deviceTestResponse {
	e.t.Helper()
	body := map[string]any{"flowHandle": handle}
	if reason != "" {
		body["reason"] = reason
	}
	return e.post("/api/oauth2/device/deny", body, call)
}

func updatedData(t *testing.T, resp deviceTestResponse) map[string]any {
	t.Helper()
	data, _ := resp.json(t)["updatedData"].(map[string]any)
	if data == nil {
		t.Fatalf("response has no updatedData: %s", resp.Body)
	}
	return data
}

func browserCode(t *testing.T, resp deviceTestResponse) string {
	t.Helper()
	code, _ := updatedData(t, resp)["code"].(string)
	return code
}

func expectBrowserError(t *testing.T, resp deviceTestResponse, status int, code string) {
	t.Helper()
	if resp.Status != status || browserCode(t, resp) != code {
		t.Fatalf("status = %d, code = %q; want %d %q (body %s)", resp.Status, browserCode(t, resp), status, code, resp.Body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
}

func (e *deviceBrowserEnv) revocations() []string {
	var queries []string
	for _, call := range e.hydra.callsTo(http.MethodDelete, "/admin/oauth2/auth/sessions/consent") {
		queries = append(queries, call.RawQuery)
	}
	return queries
}

// assertNoBrowserLeaks is the browser half of assertNoSecretLeaks.
func (e *deviceTestEnv) assertNoBrowserLeaks() {
	e.t.Helper()
	e.hydra.mu.Lock()
	userCodes := slices.Clone(e.hydra.userCodes)
	deviceCodes := slices.Clone(e.hydra.deviceCodes)
	chain := e.hydra.chain
	e.hydra.mu.Unlock()
	e.mu.Lock()
	records := slices.Clone(e.browser)
	handles := slices.Clone(e.flowHandles)
	hdcs := slices.Clone(e.issuedHDC)
	e.mu.Unlock()
	for _, record := range records {
		if record.Path == "/api/oauth2/device/lookup" && record.Status == http.StatusOK {
			var decoded struct {
				UpdatedData struct {
					FlowHandle string `json:"flowHandle"`
				} `json:"updatedData"`
			}
			if json.Unmarshal([]byte(record.Body), &decoded) == nil && decoded.UpdatedData.FlowHandle != "" {
				handles = append(handles, decoded.UpdatedData.FlowHandle)
			}
		}
	}
	var chainSecrets []string
	if chain != nil {
		chainSecrets = chain.allSecrets()
	}

	keyNames, redisDump := e.redisDump()
	logs := e.logs.String()
	for _, handle := range handles {
		if strings.Contains(logs, handle) || strings.Contains(keyNames, handle) || strings.Contains(redisDump, handle) {
			e.t.Error("a flow handle reached the logs or Redis")
		}
	}
	if strings.Contains(logs, deviceFlowHandlePrefix) {
		e.t.Error("the logs contain a flow handle prefix")
	}
	for _, secret := range chainSecrets {
		if strings.Contains(logs, secret) || strings.Contains(redisDump, secret) {
			e.t.Error("a Hydra challenge, verifier or cookie reached the logs or Redis")
		}
	}
	for _, record := range records {
		issuing := record.Path == "/api/oauth2/device/lookup" && record.Status == http.StatusOK
		for _, secret := range append(slices.Clone(hdcs), deviceCodes...) {
			if strings.Contains(record.Body, secret) {
				e.t.Errorf("%s response contains a device code", record.Path)
			}
		}
		if strings.Contains(record.Body, "ory_dc_") || strings.Contains(record.Body, deviceWrappedCodePrefix) ||
			strings.Contains(record.Body, testHydraErrorDetail) {
			e.t.Errorf("%s response contains a device code or Hydra text: %s", record.Path, record.Body)
		}
		for _, secret := range chainSecrets {
			if strings.Contains(record.Body, secret) {
				e.t.Errorf("%s response contains a Hydra challenge, verifier or cookie", record.Path)
			}
		}
		if issuing {
			continue
		}
		if strings.Contains(record.Body, deviceFlowHandlePrefix) {
			e.t.Errorf("%s response contains a flow handle: %s", record.Path, record.Body)
		}
		for _, code := range userCodes {
			if strings.Contains(record.Body, code) || strings.Contains(record.Body, formatDeviceUserCode(code)) {
				e.t.Errorf("%s response contains a user code: %s", record.Path, record.Body)
			}
		}
	}
	for _, record := range records {
		if record.Status == http.StatusUnauthorized {
			e.t.Errorf("%s answered 401", record.Path)
		}
	}
}

func (e *deviceTestEnv) redisDump() (string, string) {
	var names, dump strings.Builder
	for _, key := range e.redis.Keys() {
		names.WriteString(key + "\n")
		switch e.redis.Type(key) {
		case "hash":
			fields, _ := e.redis.HKeys(key)
			for _, field := range fields {
				dump.WriteString(field + "=" + e.redis.HGet(key, field) + "\n")
			}
		case "string":
			value, _ := e.redis.Get(key)
			dump.WriteString(value + "\n")
		case "zset":
			members, _ := e.redis.ZMembers(key)
			dump.WriteString(strings.Join(members, ",") + "\n")
		}
	}
	return names.String(), names.String() + dump.String()
}
