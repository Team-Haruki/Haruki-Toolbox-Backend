package adminoauth

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	json "encoding/json/v2"
)

const testDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// deviceOnlyCLIClient is a public device-only client as Hydra serialises it:
// empty redirect and response types, a stored device policy with a member the
// admin form does not own.
const deviceOnlyCLIClient = `{
	"client_id": "cli-1",
	"client_name": "CLI",
	"redirect_uris": [],
	"grant_types": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
	"response_types": [],
	"scope": "offline_access user:read game-data:read",
	"token_endpoint_auth_method": "none",
	"metadata": {"haruki": {"active": true, "device": {"first_party": false, "allow_write": false, "max_codes_per_10m": 120, "note": "kept"}}}
}`

// authCodeOnlyClient was registered out of band without the refresh grant.
const authCodeOnlyClient = `{
	"client_id": "web-1",
	"client_name": "Web",
	"redirect_uris": ["https://web.example.com/callback"],
	"grant_types": ["authorization_code"],
	"response_types": ["code"],
	"scope": "openid user:read",
	"token_endpoint_auth_method": "none",
	"metadata": {"haruki": {"active": true}}
}`

// writerProxyClient is a public device client already allowed to write.
const writerProxyClient = `{
	"client_id": "proxy-1",
	"client_name": "Proxy",
	"redirect_uris": [],
	"grant_types": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
	"response_types": [],
	"scope": "offline_access user:read game-data:write",
	"token_endpoint_auth_method": "none",
	"metadata": {"haruki": {"active": true, "device": {"first_party": false, "allow_write": true, "max_codes_per_10m": 60}}}
}`

func TestCreateDeviceOnlyClientWithoutRedirectURIs(t *testing.T) {
	hydra := newFakeHydraClients(t)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "bot-device",
		"name": "Bot Device",
		"clientType": "confidential",
		"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
		"devicePolicy": {"firstParty": true, "maxCodesPer10m": 600},
		"scopes": ["offline_access", "user:read", "game-data:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientCreateResponse
	decodeUpdatedData(t, envelope, &resp)
	if resp.ClientSecret == "" || !resp.DeviceEnabled {
		t.Fatalf("response = %#v, want a confidential device client with its secret", resp)
	}
	if !reflect.DeepEqual(resp.GrantTypes, []string{testDeviceGrantType, "refresh_token"}) {
		t.Fatalf("response grantTypes = %#v", resp.GrantTypes)
	}
	if want := (adminOAuthClientDevicePolicy{FirstParty: true, MaxCodesPer10m: 600}); resp.DevicePolicy != want {
		t.Fatalf("response devicePolicy = %#v, want %#v", resp.DevicePolicy, want)
	}
	// Lists are never null, also when a device-only client has no redirect URIs.
	for _, member := range []string{`"redirectUris":[]`, `"postLogoutRedirectUris":[]`} {
		if !strings.Contains(string(envelope.UpdatedData), member) {
			t.Fatalf("updatedData %s lacks %s", envelope.UpdatedData, member)
		}
	}

	requests := hydra.takeRequests()
	if got := requestLines(requests); !reflect.DeepEqual(got, []string{"POST /admin/clients"}) {
		t.Fatalf("requests = %v", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(requests[0].Body, &payload); err != nil {
		t.Fatalf("decode create payload: %v", err)
	}
	if !reflect.DeepEqual(payload["redirect_uris"], []any{}) || !reflect.DeepEqual(payload["response_types"], []any{}) {
		t.Fatalf("create payload redirect_uris = %#v, response_types = %#v; want both empty", payload["redirect_uris"], payload["response_types"])
	}
	if !reflect.DeepEqual(payload["grant_types"], []any{testDeviceGrantType, "refresh_token"}) {
		t.Fatalf("create payload grant_types = %#v", payload["grant_types"])
	}
	wantMetadata := map[string]any{"haruki": map[string]any{
		"active": true,
		"device": map[string]any{"first_party": true, "allow_write": false, "max_codes_per_10m": float64(600)},
	}}
	if !reflect.DeepEqual(payload["metadata"], wantMetadata) {
		t.Fatalf("create payload metadata = %#v, want %#v", payload["metadata"], wantMetadata)
	}

	// Without devicePolicy nothing is stored and the response shows the defaults.
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "cli-device",
		"name": "CLI Device",
		"clientType": "public",
		"redirectUris": [],
		"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code"],
		"scopes": ["user:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("second create status = %d, body = %#v", status, envelope)
	}
	resp = adminOAuthClientCreateResponse{}
	decodeUpdatedData(t, envelope, &resp)
	if want := (adminOAuthClientDevicePolicy{MaxCodesPer10m: 60}); resp.DevicePolicy != want || !resp.DeviceEnabled {
		t.Fatalf("second create response = %#v, want deviceEnabled with the default policy", resp)
	}
	requests = hydra.takeRequests()
	payload = nil // json v2 merges into a non-nil map
	if err := json.Unmarshal(requests[0].Body, &payload); err != nil {
		t.Fatalf("decode create payload: %v", err)
	}
	if !reflect.DeepEqual(payload["metadata"], map[string]any{"haruki": map[string]any{"active": true}}) {
		t.Fatalf("create payload metadata = %#v, want no device policy", payload["metadata"])
	}

	// An authorization code client still needs redirect URIs.
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "web-app",
		"name": "Web App",
		"clientType": "public",
		"scopes": ["openid", "user:read"]
	}`)
	assertAdminOAuthClientErrorCode(t, status, envelope, "redirect_uris_required")
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("a rejected payload must not reach hydra: %v", requestLines(got))
	}
}

func TestRejectUnknownGrantType(t *testing.T) {
	hydra := newFakeHydraClients(t, confidentialBotClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	for grantTypes, code := range map[string]string{
		`["authorization_code", "client_credentials"]`: "unsupported_grant_type",
		`["implicit"]`:      "unsupported_grant_type",
		`["password"]`:      "unsupported_grant_type",
		`["refresh_token"]`: "grant_type_required",
		`[]`:                "grant_type_required",
	} {
		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
			"clientId": "new-client",
			"name": "New",
			"clientType": "confidential",
			"redirectUris": ["https://new.example.com/callback"],
			"grantTypes": `+grantTypes+`,
			"scopes": ["user:read"]
		}`)
		assertAdminOAuthClientErrorCode(t, status, envelope, code)

		status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1", `{
			"name": "Bot",
			"clientType": "confidential",
			"redirectUris": ["https://bot.example.com/callback"],
			"grantTypes": `+grantTypes+`,
			"scopes": ["openid", "user:read"]
		}`)
		assertAdminOAuthClientErrorCode(t, status, envelope, code)
	}
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("rejected grant types must not reach hydra: %v", requestLines(got))
	}
}

func TestOfflineAccessRequiresRefreshGrant(t *testing.T) {
	hydra := newFakeHydraClients(t, authCodeOnlyClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	for _, body := range []string{`{
		"clientId": "web-2",
		"name": "Web 2",
		"clientType": "public",
		"redirectUris": ["https://web.example.com/callback"],
		"grantTypes": ["authorization_code"],
		"scopes": ["openid", "offline_access"]
	}`, `{
		"clientId": "cli-2",
		"name": "CLI 2",
		"clientType": "public",
		"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code"],
		"scopes": ["offline_access", "user:read"]
	}`} {
		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", body)
		assertAdminOAuthClientErrorCode(t, status, envelope, "offline_access_requires_refresh_token")
	}
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("rejected creates must not reach hydra: %v", requestLines(got))
	}

	// Omitted grantTypes: judged on the registered ones, which lack refresh_token.
	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/web-1", `{
		"name": "Web",
		"clientType": "public",
		"redirectUris": ["https://web.example.com/callback"],
		"scopes": ["openid", "user:read", "offline_access"]
	}`)
	assertAdminOAuthClientErrorCode(t, status, envelope, "offline_access_requires_refresh_token")
	if got := requestLines(hydra.takeRequests()); !reflect.DeepEqual(got, []string{"GET /admin/clients/web-1"}) {
		t.Fatalf("requests = %v, want the GET and no patch", got)
	}

	// Adding the refresh grant in the same update makes it valid.
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/web-1", `{
		"name": "Web",
		"clientType": "public",
		"redirectUris": ["https://web.example.com/callback"],
		"grantTypes": ["authorization_code", "refresh_token"],
		"scopes": ["openid", "user:read", "offline_access"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	after := hydra.client("web-1")
	if !reflect.DeepEqual(after["grant_types"], []any{"authorization_code", "refresh_token"}) || !reflect.DeepEqual(after["response_types"], []any{"code"}) {
		t.Fatalf("grant_types = %#v, response_types = %#v", after["grant_types"], after["response_types"])
	}
}

func TestDeviceWriteRequiresPublicClient(t *testing.T) {
	hydra := newFakeHydraClients(t, writerProxyClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	for name, body := range map[string]string{
		"confidential client": `{
			"clientId": "bot-w", "name": "Bot", "clientType": "confidential",
			"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
			"devicePolicy": {"allowWrite": true},
			"scopes": ["offline_access", "user:read", "game-data:write"]
		}`,
		"no device grant": `{
			"clientId": "web-w", "name": "Web", "clientType": "public",
			"redirectUris": ["https://web.example.com/callback"],
			"grantTypes": ["authorization_code", "refresh_token"],
			"devicePolicy": {"allowWrite": true},
			"scopes": ["offline_access", "user:read", "game-data:write"]
		}`,
		"no game-data:write scope": `{
			"clientId": "cli-w", "name": "CLI", "clientType": "public",
			"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
			"devicePolicy": {"allowWrite": true},
			"scopes": ["offline_access", "user:read", "game-data:read"]
		}`,
	} {
		status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", body)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, status)
		}
		assertAdminOAuthClientErrorCode(t, status, envelope, "device_write_requires_public_client")
	}
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("rejected creates must not reach hydra: %v", requestLines(got))
	}

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "proxy-2", "name": "Proxy 2", "clientType": "public",
		"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
		"devicePolicy": {"allowWrite": true},
		"scopes": ["offline_access", "user:read", "game-data:write"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("public writer status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientCreateResponse
	decodeUpdatedData(t, envelope, &resp)
	if !resp.DevicePolicy.AllowWrite {
		t.Fatalf("response devicePolicy = %#v, want allowWrite", resp.DevicePolicy)
	}
	device := hydra.client("proxy-2")["metadata"].(map[string]any)["haruki"].(map[string]any)["device"].(map[string]any)
	if device["allow_write"] != true {
		t.Fatalf("stored device policy = %#v, want allow_write", device)
	}
	hydra.takeRequests()

	// The stored allow_write counts when devicePolicy is omitted: switching the
	// writer to confidential must clear it explicitly.
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/proxy-1", `{
		"name": "Proxy", "clientType": "confidential",
		"scopes": ["offline_access", "user:read", "game-data:write"]
	}`)
	assertAdminOAuthClientErrorCode(t, status, envelope, "device_write_requires_public_client")
	if got := requestLines(hydra.takeRequests()); !reflect.DeepEqual(got, []string{"GET /admin/clients/proxy-1"}) {
		t.Fatalf("requests = %v, want the GET and no patch", got)
	}
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/proxy-1", `{
		"name": "Proxy", "clientType": "confidential",
		"devicePolicy": {"allowWrite": false},
		"scopes": ["offline_access", "user:read", "game-data:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("clearing allowWrite status = %d, body = %#v", status, envelope)
	}
}

func TestDeviceOnlyRejectsPostLogoutURIs(t *testing.T) {
	hydra := newFakeHydraClients(t, deviceOnlyCLIClient, confidentialBotClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "cli-3", "name": "CLI 3", "clientType": "public",
		"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
		"postLogoutRedirectUris": ["https://cli.example.com/bye"],
		"scopes": ["offline_access", "user:read"]
	}`)
	assertAdminOAuthClientErrorCode(t, status, envelope, "post_logout_requires_redirect_uris")
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/cli-1", `{
		"name": "CLI", "clientType": "public",
		"postLogoutRedirectUris": ["https://cli.example.com/bye"],
		"scopes": ["offline_access", "user:read"]
	}`)
	assertAdminOAuthClientErrorCode(t, status, envelope, "post_logout_requires_redirect_uris")
	for _, request := range hydra.takeRequests() {
		if request.Method != http.MethodGet {
			t.Fatalf("a rejected payload must not write to hydra: %s %s", request.Method, request.Path)
		}
	}

	// Converting an authorization code client to device-only clears its kept
	// post-logout URIs in the same patch as its redirect URIs.
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/bot-1", `{
		"name": "Bot", "clientType": "confidential",
		"redirectUris": [],
		"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"],
		"scopes": ["openid", "user:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("convert status = %d, body = %#v", status, envelope)
	}
	requests := hydra.takeRequests()
	if got := requestLines(requests); !reflect.DeepEqual(got, []string{"GET /admin/clients/bot-1", "PATCH /admin/clients/bot-1"}) {
		t.Fatalf("requests = %v", got)
	}
	paths := patchPaths(t, requests[1].Body)
	for _, want := range []string{"/redirect_uris", "/post_logout_redirect_uris", "/grant_types", "/response_types"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("patch paths %v lack %s", paths, want)
		}
	}
	after := hydra.client("bot-1")
	for _, member := range []string{"redirect_uris", "post_logout_redirect_uris", "response_types"} {
		if !reflect.DeepEqual(after[member], []any{}) {
			t.Fatalf("%s = %#v, want empty", member, after[member])
		}
	}
	var resp adminOAuthClientUpdateResponse
	decodeUpdatedData(t, envelope, &resp)
	if !resp.DeviceEnabled || !reflect.DeepEqual(resp.GrantTypes, []string{testDeviceGrantType, "refresh_token"}) {
		t.Fatalf("response = %#v", resp)
	}
}

func TestUpdateDeviceOnlyClientWithoutGrantTypesKeepsEmptyRedirects(t *testing.T) {
	for name, redirectMember := range map[string]string{"omitted": "", "empty": `"redirectUris": [],`} {
		t.Run(name, func(t *testing.T) {
			hydra := newFakeHydraClients(t, deviceOnlyCLIClient)
			app := newHydraClientHandlerTestApp(hydra.config)
			before := hydra.client("cli-1")

			status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/cli-1", `{
				"name": "CLI v2", "clientType": "public", `+redirectMember+`
				"scopes": ["offline_access", "user:read", "game-data:read", "bindings:read"]
			}`)
			if status != http.StatusOK {
				t.Fatalf("status = %d, body = %#v", status, envelope)
			}
			requests := hydra.takeRequests()
			if got := requestLines(requests); !reflect.DeepEqual(got, []string{"GET /admin/clients/cli-1", "PATCH /admin/clients/cli-1"}) {
				t.Fatalf("requests = %v", got)
			}
			paths := patchPaths(t, requests[1].Body)
			for _, unexpected := range []string{"/grant_types", "/response_types", "/token_endpoint_auth_method", "/client_secret"} {
				if slices.Contains(paths, unexpected) {
					t.Fatalf("patch paths %v must not include %s", paths, unexpected)
				}
			}
			for _, path := range paths {
				if strings.HasPrefix(path, "/metadata") {
					t.Fatalf("patch paths %v must not touch metadata without devicePolicy", paths)
				}
			}
			after := hydra.client("cli-1")
			assertClientMembersUnchanged(t, before, after, "grant_types", "response_types", "redirect_uris", "metadata", "token_endpoint_auth_method")
			if after["client_name"] != "CLI v2" {
				t.Fatalf("client_name = %#v", after["client_name"])
			}
			var resp adminOAuthClientUpdateResponse
			decodeUpdatedData(t, envelope, &resp)
			if !resp.DeviceEnabled || len(resp.RedirectURIs) != 0 || resp.DevicePolicy.MaxCodesPer10m != 120 {
				t.Fatalf("response = %#v, want the kept device grant, no redirect uris and the stored policy", resp)
			}
		})
	}
}

func TestUpdateDevicePolicyPatchesOnlyItsMembers(t *testing.T) {
	hydra := newFakeHydraClients(t, deviceOnlyCLIClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/cli-1", `{
		"name": "CLI", "clientType": "public",
		"devicePolicy": {"firstParty": true, "maxCodesPer10m": 300},
		"scopes": ["offline_access", "user:read", "game-data:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	requests := hydra.takeRequests()
	paths := patchPaths(t, requests[len(requests)-1].Body)
	var metadataPaths []string
	for _, path := range paths {
		if strings.HasPrefix(path, "/metadata") {
			metadataPaths = append(metadataPaths, path)
		}
	}
	want := []string{"/metadata/haruki/device/first_party", "/metadata/haruki/device/allow_write", "/metadata/haruki/device/max_codes_per_10m"}
	if !reflect.DeepEqual(metadataPaths, want) {
		t.Fatalf("metadata patch paths = %v, want %v", metadataPaths, want)
	}
	device := hydra.client("cli-1")["metadata"].(map[string]any)["haruki"].(map[string]any)["device"].(map[string]any)
	wantDevice := map[string]any{"first_party": true, "allow_write": false, "max_codes_per_10m": float64(300), "note": "kept"}
	if !reflect.DeepEqual(device, wantDevice) {
		t.Fatalf("stored device policy = %#v, want %#v", device, wantDevice)
	}

	for _, maxCodes := range []string{"0", "601", "-1"} {
		status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/cli-1", `{
			"name": "CLI", "clientType": "public",
			"devicePolicy": {"maxCodesPer10m": `+maxCodes+`},
			"scopes": ["offline_access", "user:read"]
		}`)
		assertAdminOAuthClientErrorCode(t, status, envelope, "invalid_device_policy")
	}
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("an invalid device policy must not reach hydra: %v", requestLines(got))
	}
}

func TestDeviceGrantRequiresUserReadScope(t *testing.T) {
	hydra := newFakeHydraClients(t, deviceOnlyCLIClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "bot-2", "name": "Bot 2", "clientType": "confidential",
		"redirectUris": ["https://bot.example.com/callback"],
		"grantTypes": ["authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"],
		"scopes": ["offline_access", "bindings:read", "game-data:read"]
	}`)
	assertAdminOAuthClientErrorCode(t, status, envelope, "device_requires_user_read")
	if got := hydra.takeRequests(); len(got) != 0 {
		t.Fatalf("a rejected create must not reach hydra: %v", requestLines(got))
	}

	// Omitted grantTypes: the registered device grant still needs user:read.
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPut, "/oauth-clients/cli-1", `{
		"name": "CLI", "clientType": "public",
		"scopes": ["offline_access", "game-data:read"]
	}`)
	assertAdminOAuthClientErrorCode(t, status, envelope, "device_requires_user_read")
	if got := requestLines(hydra.takeRequests()); !reflect.DeepEqual(got, []string{"GET /admin/clients/cli-1"}) {
		t.Fatalf("requests = %v, want the GET and no patch", got)
	}

	// email is allowed next to the device grant (it is never granted over it).
	status, envelope = doAdminOAuthClientRequest(t, app, http.MethodPost, "/oauth-clients", `{
		"clientId": "bot-3", "name": "Bot 3", "clientType": "confidential",
		"grantTypes": ["urn:ietf:params:oauth:grant-type:device_code"],
		"scopes": ["openid", "email", "user:read"]
	}`)
	if status != http.StatusOK {
		t.Fatalf("device client with email status = %d, body = %#v", status, envelope)
	}
}

func TestListClientsEchoesGrantTypesAndDevicePolicy(t *testing.T) {
	hydra := newFakeHydraClients(t, deviceOnlyCLIClient, publicViewerClient)
	app := newHydraClientHandlerTestApp(hydra.config)

	status, envelope := doAdminOAuthClientRequest(t, app, http.MethodGet, "/oauth-clients", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %#v", status, envelope)
	}
	var resp adminOAuthClientListResponse
	decodeUpdatedData(t, envelope, &resp)
	items := map[string]adminOAuthClientListItem{}
	for _, item := range resp.Items {
		items[item.ClientID] = item
	}
	cli, viewer := items["cli-1"], items["viewer-1"]
	if !cli.DeviceEnabled || cli.DevicePolicy != (adminOAuthClientDevicePolicy{MaxCodesPer10m: 120}) || !reflect.DeepEqual(cli.GrantTypes, []string{testDeviceGrantType, "refresh_token"}) {
		t.Fatalf("cli item = %#v", cli)
	}
	if viewer.DeviceEnabled || viewer.DevicePolicy != (adminOAuthClientDevicePolicy{MaxCodesPer10m: 60}) || !reflect.DeepEqual(viewer.GrantTypes, []string{"authorization_code", "refresh_token"}) {
		t.Fatalf("viewer item = %#v", viewer)
	}
}

func assertAdminOAuthClientErrorCode(t *testing.T, status int, envelope adminOAuthClientTestEnvelope, code string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 %s, body = %#v", status, code, envelope)
	}
	var data adminOAuthClientErrorData
	decodeUpdatedData(t, envelope, &data)
	if data.Code != code {
		t.Fatalf("code = %q, want %q (message %q)", data.Code, code, envelope.Message)
	}
}
