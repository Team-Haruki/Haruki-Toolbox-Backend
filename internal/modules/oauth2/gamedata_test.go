package oauth2

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"testing"
)

func TestOAuth2GameDataGrantableTypes(t *testing.T) {
	t.Parallel()

	if !postgresql.IsGrantableGameAccountDataType("suite") {
		t.Fatalf("suite should be grantable")
	}
	if !postgresql.IsGrantableGameAccountDataType(" MySekai ") {
		t.Fatalf("mysekai should be grantable")
	}
	if postgresql.IsGrantableGameAccountDataType("profile") {
		t.Fatalf("profile should not be grantable")
	}
}
