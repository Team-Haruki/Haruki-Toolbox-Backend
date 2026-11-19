package oauth2

import (
	"errors"
	"testing"

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
	if err := NewHydraRefreshGrantPurger(db)(t.Context(), "fixture-client"); err != nil {
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
			mock.ExpectExec("^BEGIN$").WillReturnResult(sqlmock.NewResult(0, 0))
			query := "SELECT pg_advisory_xact_lock"
			if !tc.exclusive {
				query += "_shared"
			}
			exec := mock.ExpectExec(query).WithArgs("client")
			if tc.fail {
				exec.WillReturnError(fixtureErr)
				mock.ExpectClose()
			} else {
				exec.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec("^ROLLBACK$").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			release, err := NewHydraClientGrantLocker(db)(t.Context(), "client", tc.exclusive)
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

func TestRefreshGrantPurgeRefusesUnknownNetwork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := NewHydraRefreshGrantPurger(db)(t.Context(), " "); err == nil {
		t.Fatal("empty client ID was accepted")
	}
	mock.ExpectQuery("SELECT .*nid FROM hydra_client").WithArgs("unknown").
		WillReturnRows(sqlmock.NewRows([]string{"nid"}).AddRow(nil))
	if err := NewHydraRefreshGrantPurger(db)(t.Context(), "unknown"); err == nil {
		t.Fatal("purge was performed without a network")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
