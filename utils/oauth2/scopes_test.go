package oauth2

import "testing"

func TestValidateScopesAllowsOfflineAccess(t *testing.T) {
	t.Parallel()

	validated, ok := ValidateScopes(
		[]string{ScopeOfflineAccess, ScopeGameDataRead},
		[]string{ScopeOfflineAccess, ScopeGameDataRead},
	)
	if !ok {
		t.Fatalf("expected offline_access scope to be accepted")
	}
	if len(validated) != 2 {
		t.Fatalf("len(validated) = %d, want 2", len(validated))
	}
	if validated[0] != ScopeOfflineAccess {
		t.Fatalf("validated[0] = %q, want %q", validated[0], ScopeOfflineAccess)
	}
}
