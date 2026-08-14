package oauth2

import (
	"reflect"
	"testing"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

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
}
