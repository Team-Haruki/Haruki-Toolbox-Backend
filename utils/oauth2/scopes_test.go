package oauth2

import "testing"

func TestAllScopesExposeStandardOIDCScopes(t *testing.T) {
	for _, scope := range []string{ScopeOpenID, ScopeProfile, ScopeEmail} {
		if description := AllScopes[scope]; description == "" {
			t.Fatalf("OIDC scope %q is not exposed", scope)
		}
	}
}
