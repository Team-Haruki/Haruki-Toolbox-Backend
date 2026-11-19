package bootstrap

import (
	"errors"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/lib/pq"
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

func TestHydraConsentSubjectCandidatesAreBoundToClientAndNetwork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT DISTINCT flow.subject FROM hydra_oauth2_flow AS flow WHERE flow.client_id = .* AND flow.nid = .* ORDER BY flow.subject LIMIT`).
		WithArgs("example-client", 12).
		WillReturnRows(sqlmock.NewRows([]string{"subject"}).AddRow("first").AddRow("second"))
	subjects, err := hydraConsentSubjects(db)(t.Context(), "example-client", 12)
	if err != nil || len(subjects) != 2 || subjects[0] != "first" {
		t.Fatalf("consent candidate subjects=%v, %v", subjects, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHydraConsentIndexDistinguishesUnavailableFromCorruptResults(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const query = "SELECT DISTINCT flow.subject"
	const client = "example-client"
	lookup := hydraConsentSubjects(db)
	mock.ExpectQuery(query).WithArgs(client, 3).WillReturnError(&pq.Error{Code: "42501"})
	if _, err := lookup(t.Context(), client, 3); !errors.Is(err, oauth2.ErrConsentIndexUnavailable) {
		t.Fatalf("restricted index returned %v", err)
	}
	unavailable := errors.New("fixture database unavailable")
	mock.ExpectQuery(query).WithArgs(client, 3).WillReturnError(unavailable)
	if _, err := lookup(t.Context(), client, 3); !errors.Is(err, unavailable) {
		t.Fatalf("query error returned %v", err)
	}
	mock.ExpectQuery(query).WithArgs(client, 3).WillReturnRows(sqlmock.NewRows([]string{"subject"}).AddRow(nil))
	if subjects, err := lookup(t.Context(), client, 3); err == nil || len(subjects) != 0 {
		t.Fatalf("invalid index row returned %v, %v", subjects, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
