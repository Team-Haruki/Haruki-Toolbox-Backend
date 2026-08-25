package upload

import (
	"testing"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
)

// A delegated write must be distinguishable from one the owner made, or an
// audit trail cannot answer "who wrote this".
func TestOAuth2UploadMethodIsItsOwnValue(t *testing.T) {
	if harukiUtils.UploadMethodOAuth2 == "" {
		t.Fatal("UploadMethodOAuth2 is empty")
	}
	for _, other := range []harukiUtils.UploadMethod{
		harukiUtils.UploadMethodManual,
		harukiUtils.UploadMethodIOSProxy,
		harukiUtils.UploadMethodIOSScript,
		harukiUtils.UploadMethodHarukiProxy,
		harukiUtils.UploadMethodInherit,
	} {
		if harukiUtils.UploadMethodOAuth2 == other {
			t.Fatalf("UploadMethodOAuth2 collides with %q", other)
		}
	}
}

// The write scope must exist, be distinct from the read scope, and carry a
// consent description — a scope with no description is invisible on the consent
// screen, so a user would be granting write access without being told.
func TestGameDataWriteScopeIsDeclaredForConsent(t *testing.T) {
	if harukiOAuth2.ScopeGameDataWrite == harukiOAuth2.ScopeGameDataRead {
		t.Fatal("the write scope is the same string as the read scope")
	}
	desc, ok := harukiOAuth2.AllScopes[harukiOAuth2.ScopeGameDataWrite]
	if !ok || desc == "" {
		t.Fatal("game-data:write has no consent description; users would grant it blind")
	}
	if !harukiOAuth2.HasScope([]string{harukiOAuth2.ScopeGameDataWrite}, harukiOAuth2.ScopeGameDataWrite) {
		t.Fatal("HasScope does not recognise the write scope")
	}
	// Holding read must NOT satisfy a write requirement.
	if harukiOAuth2.HasScope([]string{harukiOAuth2.ScopeGameDataRead}, harukiOAuth2.ScopeGameDataWrite) {
		t.Fatal("a read-only token satisfies the write scope")
	}
}

// The route must not exist without the pieces that make it safe. Registering a
// delegated WRITE with no disabled-client check would let a revoked integration
// keep overwriting player data for as long as its token lives.
func TestDelegatedUploadRouteRequiresItsGuards(t *testing.T) {
	if (Dependencies{}).HydraConfig != nil {
		t.Fatal("zero Dependencies must not carry a HydraConfig")
	}
	if (Dependencies{}).OAuth2ClientActiveChecker != nil {
		t.Fatal("zero Dependencies must not carry a client-active checker")
	}
	// Both guards are consulted by registerOAuth2UploadRoutes before the route
	// is added; a nil apiHelper would panic if it ever got past them.
	registerOAuth2UploadRoutes(nil, Dependencies{})
	registerOAuth2UploadRoutes(nil, Dependencies{HydraConfig: &harukiOAuth2.HydraConfig{}})
}
