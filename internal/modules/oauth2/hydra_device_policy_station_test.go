package oauth2

import (
	"testing"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

// station:room:write is an ordinary device-grantable scope: any client that
// registered it may request it, public or confidential, without allow_write,
// but never a client that did not register it.
func TestDevicePolicyAllowsStationScope(t *testing.T) {
	requested := normalizeDeviceScope("user:read offline_access " + harukiOAuth2.ScopeStationRoomWrite)
	registered := "user:read offline_access " + harukiOAuth2.ScopeStationRoomWrite
	for _, client := range []*HydraOAuthClient{
		{ClientID: "haruki-client", TokenEndpointAuthMethod: "none", Scope: registered},
		{ClientID: "station-cli", TokenEndpointAuthMethod: "client_secret_basic", Scope: registered},
	} {
		if description, ok := checkDeviceScopePolicy(client, requested); !ok {
			t.Fatalf("%s: station scope refused: %s", client.ClientID, description)
		}
	}

	unregistered := &HydraOAuthClient{ClientID: "other", TokenEndpointAuthMethod: "none", Scope: "user:read offline_access"}
	if _, ok := checkDeviceScopePolicy(unregistered, requested); ok {
		t.Fatal("a client without station:room:write registered was granted it")
	}
	if _, ok := harukiOAuth2.AllScopes[harukiOAuth2.ScopeStationRoomWrite]; !ok {
		t.Fatal("station:room:write is not a registered scope")
	}
}
