package adminoauth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"encoding/json/jsontext"
	json "encoding/json/v2"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

// confidentialBotClient carries fields the admin form does not own: a device
// grant, per-client lifespans, skip_consent, post-logout URIs and foreign metadata.
const confidentialBotClient = `{
	"client_id": "bot-1",
	"client_name": "Bot",
	"redirect_uris": ["https://bot.example.com/callback"],
	"post_logout_redirect_uris": ["https://bot.example.com/logged-out"],
	"grant_types": ["authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"],
	"response_types": ["code"],
	"scope": "openid user:read",
	"token_endpoint_auth_method": "client_secret_basic",
	"skip_consent": true,
	"authorization_code_grant_access_token_lifespan": "1h0m0s",
	"device_authorization_grant_access_token_lifespan": "30m0s",
	"metadata": {"haruki": {"active": true, "device": {"max_codes_per_10m": 60}}, "owner": {"team": "infra"}}
}`

const publicViewerClient = `{
	"client_id": "viewer-1",
	"client_name": "Viewer",
	"redirect_uris": ["https://viewer.example.com/callback"],
	"grant_types": ["authorization_code", "refresh_token"],
	"response_types": ["code"],
	"scope": "openid user:read",
	"token_endpoint_auth_method": "none",
	"refresh_token_grant_refresh_token_lifespan": "720h0m0s",
	"metadata": {"haruki": {"active": true}, "other": {"note": "kept"}}
}`

// Members the admin form owns; a patch must touch nothing else.
var adminOwnedClientPatchPaths = []string{
	"/client_name", "/scope", "/redirect_uris", "/post_logout_redirect_uris",
	"/grant_types", "/response_types", "/token_endpoint_auth_method", "/client_secret", "/metadata",
}

func TestSetActivePatchPreservesGrantTypesAndLifespans(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	helper := newAdminOAuthTestHelper(t)
	seedAdminOAuthTestUser(t, helper, "u-1", "kratos-1", userSchema.RoleUser)
	hydra.addConsent("kratos-1", "bot-1", "consent-1")
	app := newHydraClientHandlerTestAppAs(helper, hydra.config, "super-admin-1", adminCoreModule.RoleSuperAdmin)
	before := hydra.client("bot-1")

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1/active", `{"active":false}`)
	if status != http.StatusOK {
		t.Fatalf("deactivate status = %d, body = %#v", status, envelope)
	}
	requests := hydra.takeRequests()
	if got := requestLines(requests[:2]); !reflect.DeepEqual(got, []string{"GET /admin/clients/bot-1", "PATCH /admin/clients/bot-1"}) {
		t.Fatalf("deactivate requests = %v, want the metadata patch first", requestLines(requests))
	}
	if want := `[{"op":"add","path":"/metadata/haruki/active","value":false}]`; string(requests[1].Body) != want {
		t.Fatalf("deactivate patch = %s, want %s", requests[1].Body, want)
	}
	// The grants are revoked after the patch; TestDisableClientRevokesPerSubject covers how.
	if got := hydra.consentClients("kratos-1"); len(got) != 0 {
		t.Fatalf("consent sessions left after disable: %v", got)
	}
	after := hydra.client("bot-1")
	assertClientMembersUnchanged(t, before, after, "grant_types", "response_types", "redirect_uris", "post_logout_redirect_uris",
		"authorization_code_grant_access_token_lifespan", "device_authorization_grant_access_token_lifespan", "skip_consent", "token_endpoint_auth_method")
	metadata := after["metadata"].(map[string]any)
	if !reflect.DeepEqual(metadata["owner"], map[string]any{"team": "infra"}) {
		t.Fatalf("foreign metadata changed: %#v", metadata)
	}
	haruki := metadata["haruki"].(map[string]any)
	if haruki["active"] != false || haruki["device"] == nil {
		t.Fatalf("haruki metadata = %#v, want active=false with device kept", haruki)
	}

	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/restore", "")
	if status != http.StatusOK {
		t.Fatalf("restore status = %d, body = %#v", status, envelope)
	}
	requests = hydra.takeRequests()
	if got := requestLines(requests); !reflect.DeepEqual(got, []string{"GET /admin/clients/bot-1", "PATCH /admin/clients/bot-1"}) {
		t.Fatalf("restore requests = %v", got)
	}
	if want := `[{"op":"add","path":"/metadata/haruki/active","value":true}]`; string(requests[1].Body) != want {
		t.Fatalf("restore patch = %s, want %s", requests[1].Body, want)
	}
	restored := hydra.client("bot-1")
	assertClientMembersUnchanged(t, before, restored, "grant_types", "response_types", "post_logout_redirect_uris", "metadata",
		"authorization_code_grant_access_token_lifespan", "device_authorization_grant_access_token_lifespan", "skip_consent")
}

func TestRotateSecretUsesJSONPatch(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	app := newHydraClientHandlerTestApp(hydra.config)
	before := hydra.client("bot-1")

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/bot-1/rotate-secret", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientRotateSecretResponse
	decodeUpdatedData(t, envelope, &resp)
	if resp.ClientSecret == "" {
		t.Fatalf("rotate response has no secret: %#v", resp)
	}
	requests := hydra.takeRequests()
	if got := requestLines(requests); !reflect.DeepEqual(got, []string{"GET /admin/clients/bot-1", "PATCH /admin/clients/bot-1"}) {
		t.Fatalf("requests = %v", got)
	}
	want := fmt.Sprintf(`[{"op":"replace","path":"/client_secret","value":%q}]`, resp.ClientSecret)
	if string(requests[1].Body) != want {
		t.Fatalf("patch = %s, want %s", requests[1].Body, want)
	}
	if got := hydra.secret("bot-1"); got != resp.ClientSecret {
		t.Fatalf("hydra secret = %q, want the returned one", got)
	}
	assertClientMembersUnchanged(t, before, hydra.client("bot-1"), "grant_types", "post_logout_redirect_uris", "metadata",
		"authorization_code_grant_access_token_lifespan", "device_authorization_grant_access_token_lifespan", "skip_consent")
}

func TestRotateSecretRejectsPublicClient(t *testing.T) {
	hydra := newFakeHydraClients(t, publicViewerClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients/viewer-1/rotate-secret", "")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %#v", status, envelope)
	}
	var data adminOAuthClientErrorData
	decodeUpdatedData(t, envelope, &data)
	if data.Code != "public_client_has_no_secret" {
		t.Fatalf("code = %q, want public_client_has_no_secret", data.Code)
	}
	if got := requestLines(hydra.takeRequests()); !reflect.DeepEqual(got, []string{"GET /admin/clients/viewer-1"}) {
		t.Fatalf("requests = %v, want only the GET", got)
	}
	if got := hydra.secret("viewer-1"); got != "" {
		t.Fatalf("public client gained a secret: %q", got)
	}
}

func TestUpdateClientPatchKeepsUnknownFields(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	app := newHydraClientHandlerTestApp(hydra.config)
	before := hydra.client("bot-1")

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1", `{
		"name": "Bot v2",
		"clientType": "confidential",
		"redirectUris": ["https://bot.example.com/callback", "https://bot.example.com/callback2"],
		"postLogoutRedirectUris": ["https://bot.example.com/bye"],
		"scopes": ["openid", "user:read", "bindings:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientUpdateResponse
	decodeUpdatedData(t, envelope, &resp)
	if resp.ClientSecret != "" {
		t.Fatalf("an update that keeps the client type must not issue a secret")
	}
	if !reflect.DeepEqual(resp.PostLogoutRedirectURIs, []string{"https://bot.example.com/bye"}) {
		t.Fatalf("response postLogoutRedirectUris = %#v", resp.PostLogoutRedirectURIs)
	}

	requests := hydra.takeRequests()
	if got := requestLines(requests); !reflect.DeepEqual(got, []string{"GET /admin/clients/bot-1", "PATCH /admin/clients/bot-1"}) {
		t.Fatalf("requests = %v", got)
	}
	paths := patchPaths(t, requests[1].Body)
	for _, path := range paths {
		if !slices.Contains(adminOwnedClientPatchPaths, path) {
			t.Fatalf("patch touches %s, which the admin form does not own (paths %v)", path, paths)
		}
	}
	if !slices.Contains(paths, "/post_logout_redirect_uris") {
		t.Fatalf("patch paths %v lack /post_logout_redirect_uris", paths)
	}

	after := hydra.client("bot-1")
	assertClientMembersUnchanged(t, before, after, "grant_types", "response_types", "metadata", "skip_consent", "token_endpoint_auth_method",
		"authorization_code_grant_access_token_lifespan", "device_authorization_grant_access_token_lifespan")
	if after["client_name"] != "Bot v2" || after["scope"] != "openid user:read bindings:read" {
		t.Fatalf("owned members not updated: %#v", after)
	}
	if !reflect.DeepEqual(after["post_logout_redirect_uris"], []any{"https://bot.example.com/bye"}) {
		t.Fatalf("post_logout_redirect_uris = %#v", after["post_logout_redirect_uris"])
	}
}

func TestUpdateOmittedGrantTypesKeepsCurrent(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	app := newHydraClientHandlerTestApp(hydra.config)
	before := hydra.client("bot-1")

	// No grantTypes and no postLogoutRedirectUris: both registered lists are kept.
	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1", `{
		"name": "Bot",
		"clientType": "confidential",
		"redirectUris": ["https://bot.example.com/callback"],
		"scopes": ["openid", "user:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	requests := hydra.takeRequests()
	paths := patchPaths(t, requests[len(requests)-1].Body)
	for _, unexpected := range []string{"/grant_types", "/response_types", "/post_logout_redirect_uris", "/token_endpoint_auth_method", "/client_secret"} {
		if slices.Contains(paths, unexpected) {
			t.Fatalf("patch paths %v must not include %s", paths, unexpected)
		}
	}
	assertClientMembersUnchanged(t, before, hydra.client("bot-1"), "grant_types", "response_types", "post_logout_redirect_uris")
	var resp adminOAuthClientUpdateResponse
	decodeUpdatedData(t, envelope, &resp)
	if !reflect.DeepEqual(resp.PostLogoutRedirectURIs, []string{"https://bot.example.com/logged-out"}) {
		t.Fatalf("response postLogoutRedirectUris = %#v, want the kept list", resp.PostLogoutRedirectURIs)
	}
}

func TestSwitchToConfidentialPatchesSecret(t *testing.T) {
	hydra := newFakeHydraClients(t, publicViewerClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/viewer-1", `{
		"name": "Viewer",
		"clientType": "confidential",
		"redirectUris": ["https://viewer.example.com/callback"],
		"scopes": ["openid", "user:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientUpdateResponse
	decodeUpdatedData(t, envelope, &resp)
	if resp.ClientSecret == "" || resp.ClientType != "confidential" {
		t.Fatalf("response = %#v, want a one-time secret for the now confidential client", resp)
	}

	requests := hydra.takeRequests()
	if got := requestLines(requests); !reflect.DeepEqual(got, []string{"GET /admin/clients/viewer-1", "PATCH /admin/clients/viewer-1"}) {
		t.Fatalf("requests = %v, want the secret in the single patch", got)
	}
	var ops []map[string]any
	if err := json.Unmarshal(requests[1].Body, &ops); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	var sawMethod, sawSecret bool
	for _, op := range ops {
		switch op["path"] {
		case "/token_endpoint_auth_method":
			sawMethod = op["value"] == "client_secret_basic"
		case "/client_secret":
			sawSecret = op["op"] == "add" && op["value"] == resp.ClientSecret
		}
	}
	if !sawMethod || !sawSecret {
		t.Fatalf("patch %s must switch the auth method and add the returned secret together", requests[1].Body)
	}
	if got := hydra.secret("viewer-1"); got != resp.ClientSecret {
		t.Fatalf("hydra secret = %q, want the returned one", got)
	}
	if got := hydra.client("viewer-1")["refresh_token_grant_refresh_token_lifespan"]; got != "720h0m0s" {
		t.Fatalf("lifespan changed: %#v", got)
	}
}

func TestCreateClientRegistersPostLogoutRedirectURIs(t *testing.T) {
	hydra := newFakeHydraClients(t)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "web-app",
		"name": "Web App",
		"clientType": "public",
		"redirectUris": ["https://app.example.com/callback"],
		"postLogoutRedirectUris": ["https://app.example.com/logged-out", " https://app.example.com/logged-out "],
		"scopes": ["openid", "user:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientCreateResponse
	decodeUpdatedData(t, envelope, &resp)
	if !reflect.DeepEqual(resp.PostLogoutRedirectURIs, []string{"https://app.example.com/logged-out"}) {
		t.Fatalf("response postLogoutRedirectUris = %#v", resp.PostLogoutRedirectURIs)
	}
	requests := hydra.takeRequests()
	if got := requestLines(requests); !reflect.DeepEqual(got, []string{"POST /admin/clients"}) {
		t.Fatalf("requests = %v", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(requests[0].Body, &payload); err != nil {
		t.Fatalf("decode create payload: %v", err)
	}
	if !reflect.DeepEqual(payload["post_logout_redirect_uris"], []any{"https://app.example.com/logged-out"}) {
		t.Fatalf("create payload post_logout_redirect_uris = %#v", payload["post_logout_redirect_uris"])
	}

	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "web-app-2",
		"name": "Web App 2",
		"clientType": "public",
		"redirectUris": ["https://app.example.com/callback"],
		"postLogoutRedirectUris": ["https://elsewhere.example.com/logged-out"],
		"scopes": ["openid", "user:read"]
	}`)
	if status != http.StatusBadRequest {
		t.Fatalf("mismatched post-logout uri status = %d, want 400, body = %#v", status, envelope)
	}
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("a rejected payload must not reach hydra: %v", requestLines(got))
	}
}

func TestUpdateRejectsKeptPostLogoutURIsOutsideNewRedirectOrigins(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1", `{
		"name": "Bot",
		"clientType": "confidential",
		"redirectUris": ["https://new.example.com/callback"],
		"scopes": ["openid", "user:read"]
	}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %#v", status, envelope)
	}
	if got := requestLines(hydra.takeRequests()); !reflect.DeepEqual(got, []string{"GET /admin/clients/bot-1"}) {
		t.Fatalf("requests = %v, want no patch", got)
	}
}

func TestListClientsEchoesPostLogoutRedirectURIs(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodGet, "/oauth-clients", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientListResponse
	decodeUpdatedData(t, envelope, &resp)
	if len(resp.Items) != 1 || !reflect.DeepEqual(resp.Items[0].PostLogoutRedirectURIs, []string{"https://bot.example.com/logged-out"}) {
		t.Fatalf("items = %#v", resp.Items)
	}
}

func TestSanitizeAdminOAuthClientPostLogoutRedirectURIs(t *testing.T) {
	redirectURIs := []string{"https://app.example.com/callback", "http://localhost:8080/cb", "myapp://oauth/callback"}

	if got, err := sanitizeAdminOAuthClientPostLogoutRedirectURIs(nil, redirectURIs); err != nil || got != nil {
		t.Fatalf("nil input = %#v, %v; want nil (keep on update)", got, err)
	}
	if got, err := sanitizeAdminOAuthClientPostLogoutRedirectURIs([]string{}, redirectURIs); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty input = %#v, %v; want an empty non-nil list (clear)", got, err)
	}
	got, err := sanitizeAdminOAuthClientPostLogoutRedirectURIs([]string{
		" https://app.example.com/bye ", "https://app.example.com/bye", "http://localhost:8080/bye", "myapp://oauth/logged-out",
	}, redirectURIs)
	if err != nil {
		t.Fatalf("valid input returned error: %v", err)
	}
	if want := []string{"https://app.example.com/bye", "http://localhost:8080/bye", "myapp://oauth/logged-out"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitized = %#v, want %#v", got, want)
	}

	for name, values := range map[string][]string{
		"different host":   {"https://evil.example.com/bye"},
		"different scheme": {"http://app.example.com/bye"},
		"different port":   {"http://localhost:9090/bye"},
		"fragment":         {"https://app.example.com/bye#x"},
		"empty value":      {" "},
		"relative":         {"/bye"},
	} {
		if _, err := sanitizeAdminOAuthClientPostLogoutRedirectURIs(values, redirectURIs); err == nil {
			t.Fatalf("%s: expected an error for %v", name, values)
		}
	}
	if _, err := sanitizeAdminOAuthClientPostLogoutRedirectURIs([]string{"https://app.example.com/bye"}, nil); err == nil {
		t.Fatalf("post-logout uris without redirect uris must be rejected")
	}
}

func TestEnsureKeptPostLogoutRedirectURIsMatch(t *testing.T) {
	kept := []string{"https://bot.example.com/logged-out"}
	if err := ensureAdminOAuthClientKeptPostLogoutRedirectURIsMatch(kept, []string{"https://bot.example.com/callback"}); err != nil {
		t.Fatalf("kept uris on a redirect origin: %v", err)
	}
	if err := ensureAdminOAuthClientKeptPostLogoutRedirectURIsMatch(kept, []string{"https://new.example.com/callback"}); err == nil {
		t.Fatalf("kept uris outside every redirect origin must be rejected")
	}
	// No redirect URIs left (a device-only client): the update patch clears the kept
	// list itself, so the check must not refuse it.
	for _, redirectURIs := range [][]string{nil, {}} {
		if err := ensureAdminOAuthClientKeptPostLogoutRedirectURIsMatch(kept, redirectURIs); err != nil {
			t.Fatalf("redirect uris %#v: %v, want nil so the patch clears the kept list", redirectURIs, err)
		}
	}
}

// The fake must reject what Hydra v25.4.0 rejects, or revocation bugs pass unnoticed.
func TestFakeHydraConsentRevokeMatchesHydra(t *testing.T) {
	for query, accepted := range map[string]bool{
		"client=bot-1&all=true":                    false,
		"client=bot-1":                             false,
		"all=true":                                 false,
		"":                                         false,
		"subject=u-1":                              false,
		"subject=u-1&client=bot-1&all=true":        false,
		"consent_request_id=c-1&client=bot-1":      false,
		"consent_request_id=c-1&subject=u-1":       false,
		"subject=u-1&client=bot-1":                 true,
		"subject=u-1&all=true":                     true,
		"consent_request_id=c-1":                   true,
		"consent_request_id=c-1&all=true":          true,
		"subject=u%2B1%40example.com&client=bot-1": true,
	} {
		values, err := url.ParseQuery(query)
		if err != nil {
			t.Fatalf("parse %q: %v", query, err)
		}
		if got := fakeHydraConsentRevokeQueryAccepted(values); got != accepted {
			t.Fatalf("query %q accepted = %v, want %v", query, got, accepted)
		}
	}
}

func newHydraClientHandlerTestApp(hydraConfig *harukiOAuth2.HydraConfig) *fiber.App {
	return newHydraClientHandlerTestAppAs(nil, hydraConfig, "super-admin-1", adminCoreModule.RoleSuperAdmin)
}

// newHydraClientHandlerTestAppAs serves the handlers as the given actor. Handlers that
// list a client's grants (disable, revoke, delete) need a helper with a user database.
func newHydraClientHandlerTestAppAs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, actorUserID, actorRole string) *fiber.App {
	app := fiber.New(fiber.Config{JSONEncoder: jsoncodec.Marshal, JSONDecoder: jsoncodec.Unmarshal})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", actorUserID)
		c.Locals("userRole", actorRole)
		return c.Next()
	})
	app.Get("/oauth-clients", handleListHydraOAuthClients(apiHelper, hydraConfig))
	app.Post("/oauth-clients", handleCreateHydraOAuthClient(apiHelper, hydraConfig))
	app.Put("/oauth-clients/:client_id", handleUpdateHydraOAuthClient(apiHelper, hydraConfig))
	app.Delete("/oauth-clients/:client_id", handleDeleteHydraOAuthClient(apiHelper, hydraConfig))
	app.Put("/oauth-clients/:client_id/active", handleUpdateHydraOAuthClientActive(apiHelper, hydraConfig))
	app.Post("/oauth-clients/:client_id/restore", handleRestoreHydraOAuthClient(apiHelper, hydraConfig))
	app.Post("/oauth-clients/:client_id/revoke", handleRevokeHydraOAuthClient(apiHelper, hydraConfig))
	app.Post("/oauth-clients/:client_id/rotate-secret", handleRotateHydraOAuthClientSecret(apiHelper, hydraConfig))
	return app
}

type adminOAuthClientTestEnvelope struct {
	Status      int            `json:"status"`
	Message     string         `json:"message"`
	UpdatedData jsontext.Value `json:"updatedData"`
}

func doAdminOAuthClientRequest(t *testing.T, app *fiber.App, method, target, body string) (int, adminOAuthClientTestEnvelope) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", contentTypeApplicationJSON)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var envelope adminOAuthClientTestEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	return resp.StatusCode, envelope
}

func decodeUpdatedData(t *testing.T, envelope adminOAuthClientTestEnvelope, out any) {
	t.Helper()
	if err := json.Unmarshal(envelope.UpdatedData, out); err != nil {
		t.Fatalf("decode updatedData %s: %v", envelope.UpdatedData, err)
	}
}

type fakeHydraRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte
}

// fakeHydraClients keeps client documents the way Hydra serialises them and applies
// JSON Patch operations to them. It is stricter than Hydra where that catches
// mistakes: replace of a missing member fails, any PUT on a client fails the test,
// and a "test" op fails the test (Hydra answers it with 500). Revocation DELETEs
// get Hydra's 400 for query combinations it rejects, such as client+all=true.
// Consent sessions are kept per subject; revoking or deleting the client removes
// them, as Hydra's cascade does.
type fakeHydraClients struct {
	t        *testing.T
	config   *harukiOAuth2.HydraConfig
	mu       sync.Mutex
	clients  map[string]map[string]any
	secrets  map[string]string
	consents map[string][]fakeHydraConsent
	requests []fakeHydraRequest

	// failRevokeSubjects get 500 on consent revocation; failConsentList and
	// failTokenDelete make those endpoints answer 500.
	failRevokeSubjects map[string]bool
	failConsentList    bool
	failTokenDelete    bool
}

type fakeHydraConsent struct {
	ConsentRequestID string
	ClientID         string
}

func newFakeHydraClients(t *testing.T, clients ...string) *fakeHydraClients {
	t.Helper()
	fake := &fakeHydraClients{
		t:                  t,
		clients:            map[string]map[string]any{},
		secrets:            map[string]string{},
		consents:           map[string][]fakeHydraConsent{},
		failRevokeSubjects: map[string]bool{},
	}
	for _, raw := range clients {
		var doc map[string]any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("decode fake client: %v", err)
		}
		fake.clients[doc["client_id"].(string)] = doc
	}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)
	fake.config = harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: server.URL, RequestTimeout: 5 * time.Second})
	return fake
}

func (f *fakeHydraClients) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, fakeHydraRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body})

	clientID, isClientPath := strings.CutPrefix(r.URL.Path, "/admin/clients/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/clients":
		list := make([]map[string]any, 0, len(f.clients))
		for _, doc := range f.clients {
			list = append(list, doc)
		}
		writeFakeHydraJSON(w, http.StatusOK, list)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/clients":
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		if secret, ok := doc["client_secret"].(string); ok {
			f.secrets[doc["client_id"].(string)] = secret
			delete(doc, "client_secret")
		}
		f.clients[doc["client_id"].(string)] = doc
		writeFakeHydraJSON(w, http.StatusCreated, doc)
	case isClientPath && r.Method == http.MethodGet:
		doc, ok := f.clients[clientID]
		if !ok {
			writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		writeFakeHydraJSON(w, http.StatusOK, doc)
	case isClientPath && r.Method == http.MethodPatch:
		f.patch(w, clientID, body)
	case isClientPath && r.Method == http.MethodPut:
		f.t.Errorf("PUT %s replaces the whole client; lifecycle changes must use JSON Patch", r.URL.Path)
		writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "error"})
	case isClientPath && r.Method == http.MethodDelete:
		if _, ok := f.clients[clientID]; !ok {
			writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		delete(f.clients, clientID)
		delete(f.secrets, clientID)
		for subject := range f.consents {
			f.removeConsents(subject, func(consent fakeHydraConsent) bool { return consent.ClientID == clientID })
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && r.URL.Path == "/admin/oauth2/tokens":
		if r.URL.Query().Get("client_id") == "" {
			writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
			return
		}
		if f.failTokenDelete {
			writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/oauth2/auth/sessions/consent":
		if f.failConsentList {
			writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			return
		}
		sessions := make([]map[string]any, 0)
		for _, consent := range f.consents[r.URL.Query().Get("subject")] {
			sessions = append(sessions, map[string]any{
				"consent_request_id": consent.ConsentRequestID,
				"grant_scope":        []string{"openid"},
				"consent_request":    map[string]any{"client": map[string]any{"client_id": consent.ClientID}},
			})
		}
		writeFakeHydraJSON(w, http.StatusOK, sessions)
	case r.Method == http.MethodDelete && r.URL.Path == "/admin/oauth2/auth/sessions/consent":
		query := r.URL.Query()
		if !fakeHydraConsentRevokeQueryAccepted(query) {
			writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "error_hint": "Invalid combination of query parameters."})
			return
		}
		subject, revokeClientID, consentRequestID := query.Get("subject"), query.Get("client"), query.Get("consent_request_id")
		if f.failRevokeSubjects[subject] {
			writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "server_error"})
			return
		}
		if consentRequestID != "" {
			for owner := range f.consents {
				f.removeConsents(owner, func(consent fakeHydraConsent) bool { return consent.ConsentRequestID == consentRequestID })
			}
		} else {
			f.removeConsents(subject, func(consent fakeHydraConsent) bool { return revokeClientID == "" || consent.ClientID == revokeClientID })
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("unexpected hydra request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// fakeHydraConsentRevokeQueryAccepted mirrors Hydra v25.4.0
// consent/handler.go revokeOAuth2ConsentSessions: only consent_request_id alone,
// subject+client, or subject+all=true are accepted. Everything else, including
// client+all=true and client alone, gets 400 (T7).
func fakeHydraConsentRevokeQueryAccepted(query url.Values) bool {
	subject, clientID, consentRequestID := query.Get("subject"), query.Get("client"), query.Get("consent_request_id")
	all := query.Get("all") == "true"
	switch {
	case consentRequestID != "" && subject == "" && clientID == "":
		return true
	case consentRequestID == "" && subject != "" && clientID != "" && !all:
		return true
	case consentRequestID == "" && subject != "" && clientID == "" && all:
		return true
	default:
		return false
	}
}

func (f *fakeHydraClients) patch(w http.ResponseWriter, clientID string, body []byte) {
	doc, ok := f.clients[clientID]
	if !ok {
		writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	var ops []struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(body, &ops); err != nil {
		f.t.Errorf("patch body is not a JSON array of operations: %s", body)
		writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	working := cloneFakeHydraDoc(f.t, doc)
	secret := f.secrets[clientID]
	for _, op := range ops {
		if op.Op == "test" {
			f.t.Errorf("patch uses a test op: %s", body)
			writeFakeHydraJSON(w, http.StatusInternalServerError, map[string]any{"error": "error"})
			return
		}
		if op.Path == "/client_secret" {
			secret, _ = op.Value.(string)
			continue
		}
		if err := applyFakeHydraPatchOp(working, op.Op, op.Path, op.Value); err != nil {
			f.t.Errorf("patch %s: %v", body, err)
			writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request", "error_description": err.Error()})
			return
		}
	}
	redirects, _ := working["redirect_uris"].([]any)
	postLogout, _ := working["post_logout_redirect_uris"].([]any)
	if len(redirects) == 0 && len(postLogout) > 0 {
		writeFakeHydraJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_client_metadata"})
		return
	}
	f.clients[clientID] = working
	f.secrets[clientID] = secret
	writeFakeHydraJSON(w, http.StatusOK, working)
}

func applyFakeHydraPatchOp(doc map[string]any, op, path string, value any) error {
	if op != "add" && op != "replace" {
		return fmt.Errorf("unsupported op %q", op)
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	parent := doc
	for _, segment := range segments[:len(segments)-1] {
		next, ok := parent[segment].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: parent %q does not exist", path, segment)
		}
		parent = next
	}
	key := segments[len(segments)-1]
	if _, exists := parent[key]; !exists && op == "replace" {
		return fmt.Errorf("%s: replace of a missing member", path)
	}
	parent[key] = value
	return nil
}

func cloneFakeHydraDoc(t *testing.T, doc map[string]any) map[string]any {
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode fake client: %v", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatalf("decode fake client: %v", err)
	}
	return clone
}

func writeFakeHydraJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, value)
}

func (f *fakeHydraClients) client(clientID string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneFakeHydraDoc(f.t, f.clients[clientID])
}

func (f *fakeHydraClients) secret(clientID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.secrets[clientID]
}

func (f *fakeHydraClients) takeRequests() []fakeHydraRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	requests := f.requests
	f.requests = nil
	return requests
}

// removeConsents drops the subject's sessions that match; the caller holds f.mu.
func (f *fakeHydraClients) removeConsents(subject string, match func(fakeHydraConsent) bool) {
	f.consents[subject] = slices.DeleteFunc(f.consents[subject], match)
}

func (f *fakeHydraClients) addConsent(subject, clientID, consentRequestID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consents[subject] = append(f.consents[subject], fakeHydraConsent{ConsentRequestID: consentRequestID, ClientID: clientID})
}

// consentClients lists the clients the subject still has a consent session for.
func (f *fakeHydraClients) consentClients(subject string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	clients := make([]string, 0, len(f.consents[subject]))
	for _, consent := range f.consents[subject] {
		clients = append(clients, consent.ClientID)
	}
	slices.Sort(clients)
	return clients
}

func (f *fakeHydraClients) failRevoke(subjects ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, subject := range subjects {
		f.failRevokeSubjects[subject] = true
	}
}

func requestLines(requests []fakeHydraRequest) []string {
	lines := make([]string, 0, len(requests))
	for _, request := range requests {
		lines = append(lines, request.Method+" "+request.Path)
	}
	return lines
}

func patchPaths(t *testing.T, body []byte) []string {
	t.Helper()
	var ops []struct {
		Op   string `json:"op"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &ops); err != nil {
		t.Fatalf("decode patch %s: %v", body, err)
	}
	paths := make([]string, 0, len(ops))
	for _, op := range ops {
		paths = append(paths, op.Path)
	}
	return paths
}

func assertClientMembersUnchanged(t *testing.T, before, after map[string]any, members ...string) {
	t.Helper()
	for _, member := range members {
		if !reflect.DeepEqual(before[member], after[member]) {
			t.Fatalf("%s changed: before %#v, after %#v", member, before[member], after[member])
		}
	}
}
