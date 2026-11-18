package bootstrap

import (
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
	if err := purgeHydraRefreshGrants(db)(t.Context(), "fixture-client"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
