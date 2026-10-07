package oauth2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	json "encoding/json/v2"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

const testDeviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"

func TestBuildHydraOAuthClientPayloadSupportsOIDC(t *testing.T) {
	input := HydraOAuthClientUpsertInput{
		ClientID:     "oidc-client",
		ClientName:   "OIDC Client",
		ClientType:   oauthClientTypePublic,
		RedirectURIs: []string{"https://client.example.com/callback"},
		Scopes:       []string{harukiOAuth2.ScopeOpenID, harukiOAuth2.ScopeProfile, harukiOAuth2.ScopeEmail},
		Active:       true,
	}

	payload := buildHydraOAuthClientPayload(input)
	if got := payload["token_endpoint_auth_method"]; got != "none" {
		t.Fatalf("token_endpoint_auth_method = %#v, want %q", got, "none")
	}
	if got := payload["scope"]; got != "openid profile email" {
		t.Fatalf("scope = %#v, want %q", got, "openid profile email")
	}
	if got := payload["grant_types"]; !reflect.DeepEqual(got, []string{"authorization_code", "refresh_token"}) {
		t.Fatalf("grant_types = %#v", got)
	}
	if got := payload["response_types"]; !reflect.DeepEqual(got, []string{"code"}) {
		t.Fatalf("response_types = %#v", got)
	}
	if _, ok := payload["post_logout_redirect_uris"]; ok {
		t.Fatalf("post_logout_redirect_uris must be absent when none are given: %#v", payload)
	}
	if _, ok := payload["client_secret"]; ok {
		t.Fatalf("public client payload must not carry client_secret: %#v", payload)
	}
}

func TestBuildHydraOAuthClientPayloadCarriesGrantTypesAndPostLogoutURIs(t *testing.T) {
	payload := buildHydraOAuthClientPayload(HydraOAuthClientUpsertInput{
		ClientID:               "bot",
		ClientSecret:           " secret ",
		ClientName:             "Bot",
		ClientType:             oauthClientTypeConfidential,
		RedirectURIs:           []string{"https://bot.example.com/callback"},
		PostLogoutRedirectURIs: []string{"https://bot.example.com/logged-out"},
		Scopes:                 []string{harukiOAuth2.ScopeUserRead},
		GrantTypes:             []string{"authorization_code", testDeviceCodeGrantType, "refresh_token"},
		Active:                 true,
	})
	if got := payload["grant_types"]; !reflect.DeepEqual(got, []string{"authorization_code", testDeviceCodeGrantType, "refresh_token"}) {
		t.Fatalf("grant_types = %#v", got)
	}
	if got := payload["response_types"]; !reflect.DeepEqual(got, []string{"code"}) {
		t.Fatalf("response_types = %#v", got)
	}
	if got := payload["post_logout_redirect_uris"]; !reflect.DeepEqual(got, []string{"https://bot.example.com/logged-out"}) {
		t.Fatalf("post_logout_redirect_uris = %#v", got)
	}
	if got := payload["token_endpoint_auth_method"]; got != "client_secret_basic" {
		t.Fatalf("token_endpoint_auth_method = %#v", got)
	}
	if got := payload["client_secret"]; got != "secret" {
		t.Fatalf("client_secret = %#v", got)
	}

	deviceOnly := buildHydraOAuthClientPayload(HydraOAuthClientUpsertInput{
		ClientID:   "cli",
		ClientName: "CLI",
		ClientType: oauthClientTypePublic,
		Scopes:     []string{harukiOAuth2.ScopeUserRead},
		GrantTypes: []string{testDeviceCodeGrantType, "refresh_token"},
	})
	if got := deviceOnly["response_types"]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("device-only response_types = %#v, want empty", got)
	}
}

func TestBuildHydraOAuthClientUpdatePatch(t *testing.T) {
	publicClient := &HydraOAuthClient{ClientID: "c", TokenEndpointAuthMethod: "none"}
	confidentialClient := &HydraOAuthClient{ClientID: "c", TokenEndpointAuthMethod: "client_secret_basic"}
	postClient := &HydraOAuthClient{ClientID: "c", TokenEndpointAuthMethod: "client_secret_post"}
	base := HydraOAuthClientUpsertInput{
		ClientName:   " Name ",
		ClientType:   oauthClientTypePublic,
		RedirectURIs: []string{"https://app.example.com/cb"},
		Scopes:       []string{"openid", "user:read"},
	}
	ownedPrefix := `[{"op":"replace","path":"/client_name","value":"Name"},{"op":"replace","path":"/scope","value":"openid user:read"},{"op":"replace","path":"/redirect_uris","value":["https://app.example.com/cb"]}`

	testCases := []struct {
		name    string
		current *HydraOAuthClient
		mutate  func(*HydraOAuthClientUpsertInput)
		want    string
		wantErr error
	}{
		{
			name:    "omitted grant types and post-logout uris keep both",
			current: publicClient,
			mutate:  func(*HydraOAuthClientUpsertInput) {},
			want:    ownedPrefix + `]`,
		},
		{
			name:    "post-logout uris are written with add",
			current: publicClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.PostLogoutRedirectURIs = []string{"https://app.example.com/bye"}
			},
			want: ownedPrefix + `,{"op":"add","path":"/post_logout_redirect_uris","value":["https://app.example.com/bye"]}]`,
		},
		{
			name:    "empty post-logout list clears the member",
			current: publicClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.PostLogoutRedirectURIs = []string{}
			},
			want: ownedPrefix + `,{"op":"add","path":"/post_logout_redirect_uris","value":[]}]`,
		},
		{
			name:    "explicit device-only grant types convert the client in one patch",
			current: publicClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.RedirectURIs = nil
				in.GrantTypes = []string{testDeviceCodeGrantType, "refresh_token"}
			},
			want: `[{"op":"replace","path":"/client_name","value":"Name"},{"op":"replace","path":"/scope","value":"openid user:read"},{"op":"replace","path":"/redirect_uris","value":[]},` +
				`{"op":"add","path":"/post_logout_redirect_uris","value":[]},` +
				`{"op":"replace","path":"/grant_types","value":["` + testDeviceCodeGrantType + `","refresh_token"]},{"op":"replace","path":"/response_types","value":[]}]`,
		},
		{
			name:    "post-logout uris without redirect uris are refused",
			current: publicClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.RedirectURIs = nil
				in.PostLogoutRedirectURIs = []string{"https://app.example.com/bye"}
			},
			wantErr: ErrHydraPostLogoutRequiresRedirectURIs,
		},
		{
			name:    "switching to confidential writes the secret in the same patch",
			current: publicClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.ClientType = oauthClientTypeConfidential
				in.ClientSecret = "new-secret"
			},
			want: ownedPrefix + `,{"op":"replace","path":"/token_endpoint_auth_method","value":"client_secret_basic"},{"op":"add","path":"/client_secret","value":"new-secret"}]`,
		},
		{
			name:    "switching to confidential without a secret is refused",
			current: publicClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.ClientType = oauthClientTypeConfidential
			},
			wantErr: ErrHydraClientSecretRequired,
		},
		{
			name:    "switching to public only changes the auth method",
			current: confidentialClient,
			mutate:  func(*HydraOAuthClientUpsertInput) {},
			want:    ownedPrefix + `,{"op":"replace","path":"/token_endpoint_auth_method","value":"none"}]`,
		},
		{
			name:    "an out-of-band confidential method survives a confidential edit",
			current: postClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.ClientType = oauthClientTypeConfidential
			},
			want: ownedPrefix + `]`,
		},
		{
			name:    "an empty client type keeps the auth method",
			current: confidentialClient,
			mutate: func(in *HydraOAuthClientUpsertInput) {
				in.ClientType = ""
			},
			want: ownedPrefix + `]`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			input := base
			testCase.mutate(&input)
			ops, err := buildHydraOAuthClientUpdatePatch(testCase.current, input)
			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) {
					t.Fatalf("err = %v, want %v", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildHydraOAuthClientUpdatePatch returned error: %v", err)
			}
			if got := marshalPatchForTest(t, ops); got != testCase.want {
				t.Fatalf("patch =\n%s\nwant\n%s", got, testCase.want)
			}
		})
	}
}

func TestHydraOAuthClientSwitchesToConfidential(t *testing.T) {
	testCases := []struct {
		method     string
		clientType string
		want       bool
	}{
		{method: "none", clientType: oauthClientTypeConfidential, want: true},
		{method: "", clientType: oauthClientTypeConfidential, want: true},
		{method: "none", clientType: oauthClientTypePublic, want: false},
		{method: "client_secret_basic", clientType: oauthClientTypeConfidential, want: false},
		{method: "client_secret_basic", clientType: oauthClientTypePublic, want: false},
		{method: "none", clientType: "desktop", want: false},
	}
	for _, testCase := range testCases {
		got := HydraOAuthClientSwitchesToConfidential(&HydraOAuthClient{TokenEndpointAuthMethod: testCase.method}, testCase.clientType)
		if got != testCase.want {
			t.Fatalf("SwitchesToConfidential(%q, %q) = %v, want %v", testCase.method, testCase.clientType, got, testCase.want)
		}
	}
	if HydraOAuthClientSwitchesToConfidential(nil, oauthClientTypeConfidential) {
		t.Fatalf("nil client must not report a switch")
	}
}

func TestPatchHydraOAuthClientSendsJSONArray(t *testing.T) {
	recorder := newHydraClientRecorder(t, `{"client_id":"bot.prod-1","client_name":"x"}`)

	_, err := PatchHydraOAuthClient(context.Background(), recorder.config, " bot.prod-1 ", []HydraJSONPatchOp{
		{Op: hydraJSONPatchOpReplace, Path: "/client_name", Value: "x"},
		{Op: hydraJSONPatchOpAdd, Path: "/metadata/haruki/active", Value: false},
	})
	if err != nil {
		t.Fatalf("PatchHydraOAuthClient returned error: %v", err)
	}
	requests := recorder.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %#v, want one PATCH", requests)
	}
	got := requests[0]
	if got.method != http.MethodPatch || got.path != "/admin/clients/bot.prod-1" {
		t.Fatalf("request = %s %s, want PATCH /admin/clients/bot.prod-1", got.method, got.path)
	}
	if got.contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got.contentType)
	}
	// A false value must survive omitzero: dropping it would leave an invalid add op.
	if want := `[{"op":"replace","path":"/client_name","value":"x"},{"op":"add","path":"/metadata/haruki/active","value":false}]`; got.body != want {
		t.Fatalf("body = %s, want %s", got.body, want)
	}
}

func TestPatchHydraOAuthClientRefusesInvalidPatches(t *testing.T) {
	recorder := newHydraClientRecorder(t, `{"client_id":"c"}`)
	testCases := map[string]struct {
		clientID string
		ops      []HydraJSONPatchOp
	}{
		"test op":       {clientID: "c", ops: []HydraJSONPatchOp{{Op: "test", Path: "/client_name", Value: "x"}}},
		"remove op":     {clientID: "c", ops: []HydraJSONPatchOp{{Op: "remove", Path: "/client_name"}}},
		"relative path": {clientID: "c", ops: []HydraJSONPatchOp{{Op: hydraJSONPatchOpAdd, Path: "client_name", Value: "x"}}},
		"no ops":        {clientID: "c"},
		"no client id":  {clientID: " ", ops: []HydraJSONPatchOp{{Op: hydraJSONPatchOpAdd, Path: "/client_name", Value: "x"}}},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			if _, err := PatchHydraOAuthClient(context.Background(), recorder.config, testCase.clientID, testCase.ops); err == nil {
				t.Fatalf("expected PatchHydraOAuthClient to refuse the patch")
			}
		})
	}
	if requests := recorder.snapshot(); len(requests) != 0 {
		t.Fatalf("refused patches must not reach hydra: %#v", requests)
	}
}

func TestSetHydraOAuthClientActiveSendsOneMetadataOp(t *testing.T) {
	testCases := []struct {
		name     string
		client   string
		wantBody string
	}{
		{
			name:     "haruki namespace present",
			client:   `{"client_id":"c","metadata":{"haruki":{"active":true,"device":{"max_codes_per_10m":60}},"owner":{"id":12345678901234567890}}}`,
			wantBody: `[{"op":"add","path":"/metadata/haruki/active","value":false}]`,
		},
		{
			name:     "empty metadata",
			client:   `{"client_id":"c","metadata":{}}`,
			wantBody: `[{"op":"add","path":"/metadata/haruki","value":{"active":false}}]`,
		},
		{
			name:     "haruki namespace is not an object",
			client:   `{"client_id":"c","metadata":{"haruki":"legacy","owner":"team"}}`,
			wantBody: `[{"op":"add","path":"/metadata/haruki","value":{"active":false}}]`,
		},
		{
			name:     "metadata absent",
			client:   `{"client_id":"c"}`,
			wantBody: `[{"op":"add","path":"/metadata","value":{"haruki":{"active":false}}}]`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newHydraClientRecorder(t, testCase.client)
			if _, err := SetHydraOAuthClientActive(context.Background(), recorder.config, "c", false); err != nil {
				t.Fatalf("SetHydraOAuthClientActive returned error: %v", err)
			}
			requests := recorder.snapshot()
			// GET then exactly one PATCH; revocation is the caller's job.
			if len(requests) != 2 || requests[0].method != http.MethodGet || requests[1].method != http.MethodPatch {
				t.Fatalf("requests = %#v, want GET then PATCH", requests)
			}
			if requests[1].body != testCase.wantBody {
				t.Fatalf("patch body = %s, want %s", requests[1].body, testCase.wantBody)
			}
		})
	}
}

func TestRotateHydraOAuthClientSecretPatchesConfidentialClient(t *testing.T) {
	recorder := newHydraClientRecorder(t, `{"client_id":"c","token_endpoint_auth_method":"client_secret_basic"}`)
	if _, err := RotateHydraOAuthClientSecret(context.Background(), recorder.config, "c", " new-secret "); err != nil {
		t.Fatalf("RotateHydraOAuthClientSecret returned error: %v", err)
	}
	requests := recorder.snapshot()
	if len(requests) != 2 || requests[1].method != http.MethodPatch {
		t.Fatalf("requests = %#v, want GET then PATCH", requests)
	}
	if want := `[{"op":"replace","path":"/client_secret","value":"new-secret"}]`; requests[1].body != want {
		t.Fatalf("patch body = %s, want %s", requests[1].body, want)
	}
}

func TestRotateHydraOAuthClientSecretRejectsPublicClient(t *testing.T) {
	recorder := newHydraClientRecorder(t, `{"client_id":"c","token_endpoint_auth_method":"none"}`)
	_, err := RotateHydraOAuthClientSecret(context.Background(), recorder.config, "c", "new-secret")
	if !errors.Is(err, ErrHydraPublicClientHasNoSecret) {
		t.Fatalf("err = %v, want ErrHydraPublicClientHasNoSecret", err)
	}
	requests := recorder.snapshot()
	if len(requests) != 1 || requests[0].method != http.MethodGet {
		t.Fatalf("requests = %#v, want only the GET", requests)
	}
}

type recordedHydraClientRequest struct {
	method      string
	path        string
	contentType string
	body        string
}

type hydraClientRecorder struct {
	config   *harukiOAuth2.HydraConfig
	mu       sync.Mutex
	requests []recordedHydraClientRequest
}

// newHydraClientRecorder serves client for every GET and PATCH on /admin/clients/*
// and records each request.
func newHydraClientRecorder(t *testing.T, client string) *hydraClientRecorder {
	t.Helper()
	recorder := &hydraClientRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, recordedHydraClientRequest{
			method:      r.Method,
			path:        r.URL.EscapedPath(),
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		})
		recorder.mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/admin/clients/") || (r.Method != http.MethodGet && r.Method != http.MethodPatch) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(client))
	}))
	t.Cleanup(server.Close)
	recorder.config = harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: server.URL, RequestTimeout: 5 * time.Second})
	return recorder
}

func (r *hydraClientRecorder) snapshot() []recordedHydraClientRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedHydraClientRequest(nil), r.requests...)
}

func marshalPatchForTest(t *testing.T, ops []HydraJSONPatchOp) string {
	t.Helper()
	encoded, err := json.Marshal(ops, json.Deterministic(true))
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	return string(encoded)
}
