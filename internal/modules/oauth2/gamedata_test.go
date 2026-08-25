package oauth2

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"testing"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

func TestOAuth2GameDataGrantableTypes(t *testing.T) {
	t.Parallel()

	if !postgresql.IsGrantableGameAccountDataType("suite") {
		t.Fatalf("suite should be grantable")
	}
	if !postgresql.IsGrantableGameAccountDataType(" MySekai ") {
		t.Fatalf("mysekai should be grantable")
	}
	if !postgresql.IsGrantableGameAccountDataType("profile") {
		t.Fatalf("profile should be grantable")
	}
	// The OAuth2 surface still cannot ask for profile: it parses the data type
	// with ParseUploadDataType, which knows only the stored upload types. Making
	// profile grantable therefore widens the browser surface only, and does not
	// silently extend what a game-data:read token can reach.
	if _, err := harukiUtils.ParseUploadDataType("profile"); err == nil {
		t.Fatalf("OAuth2 game-data must not accept profile as a data type")
	}
	if postgresql.IsGrantableGameAccountDataType("recommend-data") {
		t.Fatalf("recommend-data is a derived capability, not a grantable type")
	}
}
