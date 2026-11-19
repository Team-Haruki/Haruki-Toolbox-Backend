package bootstrap

import (
	"errors"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPurgeHydraRefreshGrantsScopesToClientAndNetwork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const network = "00000000-0000-0000-0000-000000000001"
	mock.ExpectQuery("SELECT .*nid FROM hydra_client WHERE id").WithArgs("fixture-client").WillReturnRows(sqlmock.NewRows([]string{"nid"}).AddRow(network))
	mock.ExpectExec("DELETE FROM hydra_oauth2_refresh WHERE client_id = .* AND nid =").WithArgs("fixture-client", network).WillReturnResult(sqlmock.NewResult(0, 2))
	if err := purgeHydraRefreshGrants(db)(t.Context(), "fixture-client"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHydraGrantLocksReleaseAfterSuccessOrFailure(t *testing.T) {
	fixtureErr := errors.New("fixture lock unavailable")
	for _, tc := range []struct {
		name            string
		exclusive, fail bool
	}{
		{"shared", false, false},
		{"exclusive", true, false},
		{"lock failure", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			query := "SELECT pg_advisory_xact_lock"
			if !tc.exclusive {
				query += "_shared"
			}
			exec := mock.ExpectExec(query).WithArgs("client")
			if tc.fail {
				exec.WillReturnError(fixtureErr)
			} else {
				exec.WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectRollback()
			release, err := lockHydraClientGrants(db)(t.Context(), "client", tc.exclusive)
			if tc.fail {
				if !errors.Is(err, fixtureErr) {
					t.Fatalf("lock returned %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				release()
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

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

func TestRefreshGrantPurgeRefusesUnknownNetwork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := purgeHydraRefreshGrants(db)(t.Context(), " "); err == nil {
		t.Fatal("empty client ID was accepted")
	}
	mock.ExpectQuery("SELECT .*nid FROM hydra_client").WithArgs("unknown").
		WillReturnRows(sqlmock.NewRows([]string{"nid"}).AddRow(nil))
	if err := purgeHydraRefreshGrants(db)(t.Context(), "unknown"); err == nil {
		t.Fatal("purge was performed without a network")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
