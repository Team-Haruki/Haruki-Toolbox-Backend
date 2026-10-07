package oauth2

import (
	"slices"
	"strings"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

// deviceFlowGrantableScopes may be requested through a device flow when the
// client has them registered. game-data:write is added only for public clients
// whose device policy allows writes; email is never grantable.
// station:room:write is an ordinary scope: any client registered with it may
// request it, public or confidential, without allow_write.
var deviceFlowGrantableScopes = []string{
	harukiOAuth2.ScopeOpenID,
	harukiOAuth2.ScopeProfile,
	harukiOAuth2.ScopeOfflineAccess,
	harukiOAuth2.ScopeUserRead,
	harukiOAuth2.ScopeBindingsRead,
	harukiOAuth2.ScopeGameDataRead,
	harukiOAuth2.ScopeStationRoomWrite,
}

const deviceScopeUserReadRequired = "user:read is required for device authorization"

// normalizeDeviceScope splits a space-delimited scope, drops duplicates and
// sorts it, so the stored flow scope compares as a set.
func normalizeDeviceScope(raw string) []string {
	scopes := strings.Fields(raw)
	slices.Sort(scopes)
	return slices.Compact(scopes)
}

// deviceClientUsable reports whether a client may run device flows at all: on
// the optional allowlist, enabled, and holding the device grant. Callers answer
// every failure the same way, so a client's state is not revealed piecemeal.
func deviceClientUsable(cfg DeviceFlowConfig, client *HydraOAuthClient) bool {
	return client != nil &&
		cfg.ClientAllowed(client.ClientID) &&
		HydraOAuthClientActive(client) &&
		HydraOAuthClientDeviceEnabled(client)
}

// checkDeviceScopePolicy applies the device scope policy (ory-suite-usage §10.5.11) to a
// normalized scope list and returns the RFC 6749 error description on failure.
// The flow scope must be non-empty, contain user:read (devices echo "authorized
// as <name>"), avoid email, be registered on the client, and stay inside the
// device-grantable set.
func checkDeviceScopePolicy(client *HydraOAuthClient, scopes []string) (string, bool) {
	if len(scopes) == 0 {
		return "scope is required", false
	}
	if slices.Contains(scopes, harukiOAuth2.ScopeEmail) {
		return "email cannot be granted through device authorization", false
	}
	if !slices.Contains(scopes, harukiOAuth2.ScopeUserRead) {
		return deviceScopeUserReadRequired, false
	}
	registered := HydraOAuthClientScopes(client)
	clientType := HydraClientTypeFromAuthMethod(client.TokenEndpointAuthMethod)
	allowWrite := clientType == oauthClientTypePublic && HydraOAuthClientDevicePolicyOf(client).AllowWrite
	for _, scope := range scopes {
		if !slices.Contains(registered, scope) {
			return "requested scope is not registered for this client", false
		}
		if slices.Contains(deviceFlowGrantableScopes, scope) {
			continue
		}
		if scope == harukiOAuth2.ScopeGameDataWrite && allowWrite {
			continue
		}
		return "requested scope cannot be granted through device authorization", false
	}
	return "", true
}
