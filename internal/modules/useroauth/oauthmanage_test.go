package useroauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"

	"encoding/json/jsontext"
	json "encoding/json/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

const (
	testUserID          = "u-1"
	testIdentityID      = "kratos-1"
	testHydraDetail     = "hydra-internal-detail"
	testAuthorizations  = "/api/user/" + testUserID + "/oauth2/authorizations"
	testBrowserURL      = "https://hydra.example.com/oauth2/auth?client_id=web-app&response_type=code"
	testDeviceVerifyURL = "https://hydra.example.com/oauth2/device/verify?client_id=haruki-client&haruki_dfl=0123&user_code=%2A%2A%2A%2A"
)

type fakeHydraCall struct {
	Method   string
	RawQuery string
}

// fakeConsentHydra serves GET /admin/oauth2/auth/sessions/consent per subject
// and records every call, so a test can assert which admin writes were (not)
// issued.
type fakeConsentHydra struct {
	sessions     map[string]string // subject -> raw JSON array
	revokeStatus int

	mu    sync.Mutex
	calls []fakeHydraCall
}

func (f *fakeConsentHydra) start(t *testing.T) *harukiOAuth2.HydraConfig {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, fakeHydraCall{Method: r.Method, RawQuery: r.URL.RawQuery})
		f.mu.Unlock()
		if r.URL.Path != "/admin/oauth2/auth/sessions/consent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			body, ok := f.sessions[r.URL.Query().Get("subject")]
			if !ok {
				body = "[]"
			}
			_, _ = io.WriteString(w, body)
		case http.MethodDelete:
			if f.revokeStatus != 0 {
				w.WriteHeader(f.revokeStatus)
				_, _ = io.WriteString(w, `{"error":"server_error","error_description":"`+testHydraDetail+`"}`)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	return harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{Provider: "hydra", AdminURL: server.URL, RequestTimeout: 5 * time.Second})
}

func (f *fakeConsentHydra) callsWith(method string) []fakeHydraCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []fakeHydraCall
	for _, call := range f.calls {
		if call.Method == method {
			matched = append(matched, call)
		}
	}
	return matched
}

// testConsentSessions is what Hydra lists for the signed-in user: a browser
// authorization of web-app, a device authorization of haruki-client carrying
// the context a device approval writes, and a second haruki-client device
// recognized only by its request_url; the legacy subject holds one more.
var testConsentSessions = map[string]string{
	testIdentityID: `[
		{"consent_request_id":"crid-web","grant_scope":["user:read"],"handled_at":"2026-10-07T08:00:00Z",
		 "consent_request":{"request_url":"` + testBrowserURL + `","client":{"client_id":"web-app","client_name":"Web App","token_endpoint_auth_method":"client_secret_basic"}}},
		{"consent_request_id":"crid-device-1","grant_scope":["user:read","offline_access"],"handled_at":"2026-10-07T08:01:00Z",
		 "consent_request":{"request_url":"` + testDeviceVerifyURL + `","client":{"client_id":"haruki-client","client_name":"Haruki Client","token_endpoint_auth_method":"none"}},
		 "context":{"haruki":{"flow":"device","device_flow_id":"0123","label":"Haruki-Client @ home-server","label_source":"device","approved_via":"device-bff/v1"}}},
		{"consent_request_id":"crid-device-2","grant_scope":[],
		 "consent_request":{"request_url":"` + testDeviceVerifyURL + `","client":{"client_id":"haruki-client","client_name":"Haruki Client","token_endpoint_auth_method":"none"}}}
	]`,
	testUserID: `[
		{"consent_request_id":"crid-legacy","grant_scope":["bindings:read"],
		 "consent_request":{"request_url":"` + testBrowserURL + `","client":{"client_id":"web-app","client_name":"Web App"}},"context":null}
	]`,
}

type testEnv struct {
	app       *fiber.App
	hydra     *fakeConsentHydra
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	entClient := enttest.Open(t, "sqlite3", "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = entClient.Close() })
	env := &testEnv{
		hydra:     &fakeConsentHydra{sessions: testConsentSessions},
		apiHelper: &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: entClient}},
	}
	hydraConfig := env.hydra.start(t)
	env.app = fiber.New()
	// Stands in for the RequireAuthenticatedSelf group guard.
	env.app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", testUserID)
		c.Locals("identityID", testIdentityID)
		return c.Next()
	})
	env.app.Get(testAuthorizations+"/", handleListOAuthAuthorizations(env.apiHelper, hydraConfig))
	env.app.Delete("/api/user/:toolbox_user_id/oauth2/authorizations/:client_id/consents/:consent_request_id", handleRevokeOAuthAuthorizationConsent(env.apiHelper, hydraConfig))
	return env
}

type testEnvelope struct {
	Status      int            `json:"status"`
	Message     string         `json:"message"`
	UpdatedData jsontext.Value `json:"updatedData"`
}

type testResponse struct {
	Status   int
	Header   http.Header
	Body     string
	Envelope testEnvelope
}

func (e *testEnv) do(t *testing.T, method, path string) testResponse {
	t.Helper()
	resp, err := e.app.Test(httptest.NewRequest(method, path, nil), fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := testResponse{Status: resp.StatusCode, Header: resp.Header, Body: string(raw)}
	if err := json.Unmarshal(raw, &out.Envelope); err != nil {
		t.Fatalf("%s %s: body %q is not an envelope: %v", method, path, raw, err)
	}
	return out
}

func (r testResponse) code(t *testing.T) string {
	t.Helper()
	var data struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(r.Envelope.UpdatedData, &data); err != nil {
		t.Fatalf("updatedData %q: %v", r.Envelope.UpdatedData, err)
	}
	return data.Code
}

func (e *testEnv) audits(t *testing.T) []map[string]any {
	t.Helper()
	rows, err := e.apiHelper.DBManager.DB.SystemLog.Query().Where(systemlog.ActionEQ(oauthAuditActionRevokeConsent)).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		metadata := map[string]any{"result": string(row.Result)}
		for key, value := range row.Metadata {
			metadata[key] = value
		}
		out = append(out, metadata)
	}
	return out
}

func TestListAuthorizationsExposesFlowTypeAndLabel(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, testAuthorizations+"/")
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.Status, resp.Body)
	}
	// Decode loosely so that a missing member is told apart from an empty one.
	var items []map[string]any
	if err := json.Unmarshal(resp.Envelope.UpdatedData, &items); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"crid-web":      {"browser", ""},
		"crid-device-1": {"device", "Haruki-Client @ home-server"},
		"crid-device-2": {"device", ""},
		"crid-legacy":   {"browser", ""},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %v", items)
	}
	for _, item := range items {
		crid, _ := item["consentRequestId"].(string)
		expected, ok := want[crid]
		if !ok {
			t.Fatalf("unexpected item %v", item)
		}
		flowType, hasFlowType := item["flowType"].(string)
		label, hasLabel := item["deviceLabel"].(string)
		if !hasFlowType || !hasLabel {
			t.Fatalf("%s: flowType and deviceLabel must always be present as strings: %v", crid, item)
		}
		if flowType != expected[0] || label != expected[1] {
			t.Errorf("%s: flowType=%q deviceLabel=%q, want %q %q", crid, flowType, label, expected[0], expected[1])
		}
		if _, ok := item["scopes"].([]any); !ok {
			t.Errorf("%s: scopes must be a list, got %#v", crid, item["scopes"])
		}
	}
	if calls := env.hydra.callsWith(http.MethodDelete); len(calls) != 0 {
		t.Fatalf("listing issued Hydra writes: %v", calls)
	}
}

func TestRevokeConsentRequiresOwnershipWithoutCallingHydra(t *testing.T) {
	env := newTestEnv(t)
	for _, tc := range []struct{ name, clientID, consentRequestID string }{
		// Another user's authorization: Hydra would answer 204 and revoke it.
		{"foreign consent request", "haruki-client", "crid-someone-else"},
		// The caller's own consent request ID under another client.
		{"client mismatch", "web-app", "crid-device-1"},
		{"unknown client", "other-client", "crid-web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.do(t, http.MethodDelete, testAuthorizations+"/"+tc.clientID+"/consents/"+tc.consentRequestID)
			if resp.Status != http.StatusNotFound || resp.code(t) != oauthCodeAuthorizationNotFound {
				t.Fatalf("status = %d, body %s; want 404 authorization_not_found", resp.Status, resp.Body)
			}
			if resp.Header.Get(fiber.HeaderCacheControl) != "no-store" {
				t.Fatalf("Cache-Control = %q", resp.Header.Get(fiber.HeaderCacheControl))
			}
		})
	}
	if calls := env.hydra.callsWith(http.MethodDelete); len(calls) != 0 {
		t.Fatalf("an unowned authorization reached Hydra: %v", calls)
	}
	// Both subjects were checked before refusing.
	if gets := env.hydra.callsWith(http.MethodGet); len(gets) != 6 {
		t.Fatalf("consent session lists = %d, want 2 per request", len(gets))
	}
	audits := env.audits(t)
	if len(audits) != 3 {
		t.Fatalf("audits = %v", audits)
	}
	for _, audit := range audits {
		if audit["result"] != harukiAPIHelper.SystemLogResultFailure || audit["reason"] != "authorization_not_found" {
			t.Fatalf("audit = %v", audit)
		}
	}
}

func TestRevokeConsentUsesConsentRequestIDOnly(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodDelete, testAuthorizations+"/haruki-client/consents/crid-device-2")
	if resp.Status != http.StatusOK || resp.Header.Get(fiber.HeaderCacheControl) != "no-store" {
		t.Fatalf("status = %d, Cache-Control = %q, body %s", resp.Status, resp.Header.Get(fiber.HeaderCacheControl), resp.Body)
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Envelope.UpdatedData, &data); err != nil || data["revoked"] != true || len(data) != 1 {
		t.Fatalf("updatedData = %s (%v), want {\"revoked\":true}", resp.Envelope.UpdatedData, err)
	}
	deletes := env.hydra.callsWith(http.MethodDelete)
	// Only consent_request_id: no subject, client or all, so the call cannot
	// widen to the user's other devices of the same client.
	if len(deletes) != 1 || deletes[0].RawQuery != "consent_request_id=crid-device-2" {
		t.Fatalf("Hydra revocations = %v", deletes)
	}
	audits := env.audits(t)
	if len(audits) != 1 || audits[0]["result"] != harukiAPIHelper.SystemLogResultSuccess || audits[0]["consentRequestId"] != "crid-device-2" || audits[0]["clientID"] != "haruki-client" {
		t.Fatalf("audits = %v", audits)
	}

	// A Hydra failure is 502 revoke_failed and never relays Hydra's text.
	env.hydra.revokeStatus = http.StatusInternalServerError
	resp = env.do(t, http.MethodDelete, testAuthorizations+"/web-app/consents/crid-legacy")
	if resp.Status != http.StatusBadGateway || resp.code(t) != oauthCodeRevokeFailed || resp.Header.Get(fiber.HeaderCacheControl) != "no-store" {
		t.Fatalf("status = %d, body %s; want 502 revoke_failed", resp.Status, resp.Body)
	}
	if strings.Contains(resp.Body, testHydraDetail) {
		t.Fatal("the response relays Hydra's error text")
	}
	if deletes := env.hydra.callsWith(http.MethodDelete); len(deletes) != 2 || deletes[1].RawQuery != "consent_request_id=crid-legacy" {
		t.Fatalf("Hydra revocations = %v", deletes)
	}
}
