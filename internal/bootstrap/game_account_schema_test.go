package bootstrap

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCanonicalBindingMigrationRefusesAmbiguousOwnership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	constraint := func() {
		mock.ExpectQuery("SELECT EXISTS .*pg_constraint").WithArgs(canonicalBindingConstraint).WillReturnRows(sqlmock.NewRows([]string{"installed"}).AddRow(false))
	}
	constraint()
	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE game_account_bindings, game_account_data_grants").WillReturnResult(sqlmock.NewResult(0, 0))
	constraint()
	mock.ExpectQuery("WHERE NOT pg_input_is_valid").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("conflicting_accounts").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()
	if err := ensureCanonicalGameAccountBindings(t.Context(), db, true); err == nil || !strings.Contains(err.Error(), "conflicting numeric game accounts") {
		t.Fatalf("unexpected migration outcome: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalBindingMigrationUpdatesOwnedGrantsBeforeEnforcingConstraint(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectConstraint := func(installed bool) {
		mock.ExpectQuery("SELECT EXISTS .*pg_constraint").WithArgs(canonicalBindingConstraint).
			WillReturnRows(sqlmock.NewRows([]string{"installed"}).AddRow(installed))
	}
	expectConstraint(false)
	mock.ExpectBegin()
	mock.ExpectExec("LOCK TABLE game_account_bindings, game_account_data_grants").WillReturnResult(sqlmock.NewResult(0, 0))
	expectConstraint(false)
	for _, query := range []string{"WHERE NOT pg_input_is_valid", "conflicting_accounts"} {
		mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	}
	mock.ExpectExec("UPDATE game_account_data_grants AS g .* b.user_game_account_bindings = g.owner_user_id .* b.server = g.server AND b.game_user_id = g.game_user_id").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE game_account_bindings SET game_user_id").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("ALTER TABLE game_account_bindings ADD CONSTRAINT " + canonicalBindingConstraint).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	if err := ensureCanonicalGameAccountBindings(t.Context(), db, true); err != nil {
		t.Fatal(err)
	}
	expectConstraint(true)
	if err := ensureCanonicalGameAccountBindings(t.Context(), db, false); err != nil {
		t.Fatalf("already migrated database should pass validation: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalBindingMigrationRollsBackIncompleteChanges(t *testing.T) {
	fail := errors.New("fixture SQL failure")
	for _, phase := range []string{"lock", "invalid legacy ID", "grants", "bindings", "constraint"} {
		t.Run(phase, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			t.Cleanup(func() {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Error(err)
				}
			})
			expectConstraint := func() {
				mock.ExpectQuery("SELECT EXISTS .*pg_constraint").WithArgs(canonicalBindingConstraint).
					WillReturnRows(sqlmock.NewRows([]string{"installed"}).AddRow(false))
			}
			expectConstraint()
			mock.ExpectBegin()
			transact := func(step, query string) bool {
				expect := mock.ExpectExec(query)
				if phase == step {
					expect.WillReturnError(fail)
					return false
				}
				expect.WillReturnResult(sqlmock.NewResult(0, 0))
				return true
			}
			if transact("lock", "LOCK TABLE") {
				expectConstraint()
				invalid := 0
				if phase == "invalid legacy ID" {
					invalid = 1
				}
				mock.ExpectQuery("WHERE NOT pg_input_is_valid").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(invalid))
				if invalid == 0 {
					mock.ExpectQuery("conflicting_accounts").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
					if transact("grants", "UPDATE game_account_data_grants") && transact("bindings", "UPDATE game_account_bindings") {
						transact("constraint", "ALTER TABLE game_account_bindings ADD CONSTRAINT")
					}
				}
			}
			mock.ExpectRollback()
			err = ensureCanonicalGameAccountBindings(t.Context(), db, true)
			if phase == "invalid legacy ID" {
				if err == nil || !strings.Contains(err.Error(), "invalid IDs") {
					t.Fatalf("migration accepted invalid legacy ID: %v", err)
				}
			} else if !errors.Is(err, fail) {
				t.Fatalf("migration lost %s error: %v", phase, err)
			}
		})
	}
}

func TestCanonicalBindingValidationRequiresInstalledConstraint(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT EXISTS .*pg_constraint").WithArgs(canonicalBindingConstraint).
		WillReturnRows(sqlmock.NewRows([]string{"installed"}).AddRow(false))
	if err := ensureCanonicalGameAccountBindings(t.Context(), db, false); err == nil || !strings.Contains(err.Error(), "constraint is missing") {
		t.Fatalf("missing canonical constraint: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
