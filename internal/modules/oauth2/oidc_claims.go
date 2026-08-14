package oauth2

import (
	"strings"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

func buildHydraOIDCIDTokenClaims(userID, name, email string, emailVerified bool, grantedScopes []string) map[string]any {
	claims := map[string]any{
		// Keep the existing local Toolbox identifier for API clients that already
		// consume it. Hydra remains responsible for the standard OIDC sub claim.
		"uid": strings.TrimSpace(userID),
	}
	if harukiOAuth2.HasScope(grantedScopes, harukiOAuth2.ScopeProfile) {
		claims["name"] = name
	}
	if harukiOAuth2.HasScope(grantedScopes, harukiOAuth2.ScopeEmail) && strings.TrimSpace(email) != "" {
		claims["email"] = email
		claims["email_verified"] = emailVerified
	}
	return claims
}
