package public

import (
	"haruki-suite/ent/schema"
	harukiUtils "haruki-suite/utils"
	"haruki-suite/utils/database/postgresql"
	"testing"
)

func TestValidatePublicAPIAccessRequiresVerifiedBinding(t *testing.T) {
	t.Parallel()

	suiteEnabled := &postgresql.GameAccountBinding{
		Verified: true,
		Edges: postgresql.GameAccountBindingEdges{
			User: &postgresql.User{Banned: false},
		},
		Suite: &schema.SuiteDataPrivacySettings{
			AllowPublicApi: true,
		},
	}
	if !validatePublicAPIAccess(suiteEnabled, harukiUtils.UploadDataTypeSuite) {
		t.Fatalf("expected verified suite binding with public api enabled to be allowed")
	}

	suiteUnverified := &postgresql.GameAccountBinding{
		Verified: false,
		Edges: postgresql.GameAccountBindingEdges{
			User: &postgresql.User{Banned: false},
		},
		Suite: &schema.SuiteDataPrivacySettings{
			AllowPublicApi: true,
		},
	}
	if validatePublicAPIAccess(suiteUnverified, harukiUtils.UploadDataTypeSuite) {
		t.Fatalf("expected unverified suite binding to be denied")
	}

	mysekaiUnverified := &postgresql.GameAccountBinding{
		Verified: false,
		Edges: postgresql.GameAccountBindingEdges{
			User: &postgresql.User{Banned: false},
		},
		Mysekai: &schema.MysekaiDataPrivacySettings{
			AllowPublicApi: true,
		},
	}
	if validatePublicAPIAccess(mysekaiUnverified, harukiUtils.UploadDataTypeMysekai) {
		t.Fatalf("expected unverified mysekai binding to be denied")
	}

	suiteOwnerBanned := &postgresql.GameAccountBinding{
		Verified: true,
		Edges: postgresql.GameAccountBindingEdges{
			User: &postgresql.User{Banned: true},
		},
		Suite: &schema.SuiteDataPrivacySettings{
			AllowPublicApi: true,
		},
	}
	if validatePublicAPIAccess(suiteOwnerBanned, harukiUtils.UploadDataTypeSuite) {
		t.Fatalf("expected banned owner to be denied")
	}

	suiteOwnerMissing := &postgresql.GameAccountBinding{
		Verified: true,
		Suite: &schema.SuiteDataPrivacySettings{
			AllowPublicApi: true,
		},
	}
	if validatePublicAPIAccess(suiteOwnerMissing, harukiUtils.UploadDataTypeSuite) {
		t.Fatalf("expected missing owner to be denied")
	}
}
