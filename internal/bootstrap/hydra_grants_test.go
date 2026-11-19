package bootstrap

import (
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestHydraGrantStoreWiring(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tc := range []struct {
		dsn       string
		resources int
	}{
		{"", 0},
		{"postgres://fixture:fixture@127.0.0.1/fixture?sslmode=disable", 1},
	} {
		var cfg harukiConfig.Config
		cfg.OAuth2.HydraGrantStoreURL = tc.dsn
		app := &Application{}
		client, err := app.buildHydraProviderConfig(cfg, db)
		if err != nil || len(app.resourceClosers) != tc.resources {
			t.Fatalf("resource count=%d, configuration error=%v", len(app.resourceClosers), err)
		}
		mock.ExpectExec("^BEGIN$").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("SELECT pg_advisory_xact_lock_shared").WithArgs("client").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("^ROLLBACK$").WillReturnResult(sqlmock.NewResult(0, 0))
		if !client.ProtectClientGrants() {
			t.Fatal("token handling was not linked to the grant lock")
		}
		release, err := client.LockClientGrants(t.Context(), "client", false)
		if err != nil {
			t.Fatalf("reserve a grant through lock pool: %v", err)
		}
		release()
		for _, closer := range app.resourceClosers {
			if err := closer.close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
