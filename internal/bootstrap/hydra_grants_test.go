package bootstrap

import (
	"errors"
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
		mock.ExpectBegin().WillReturnError(errors.New("fixture toolbox database unavailable"))
		if !client.ProtectClientGrants() {
			t.Fatal("token handling was not linked to the grant lock")
		}
		if _, err := client.LockClientGrants(t.Context(), "client", false); err == nil {
			t.Fatal("token handling bypassed the Toolbox lock failure")
		}
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
