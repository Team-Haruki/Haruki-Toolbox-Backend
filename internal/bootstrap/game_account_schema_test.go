package bootstrap

import (
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
