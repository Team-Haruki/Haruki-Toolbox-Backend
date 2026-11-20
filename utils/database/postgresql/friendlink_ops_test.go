package postgresql_test

import (
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
)

func TestFriendLinkPageBindsPostgresTagByteLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	client := dbManager.NewClient(dbManager.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	mock.ExpectQuery(`SELECT .* FROM "friend_links" ORDER BY`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "description", "avatar", "url", "sort_order"}).AddRow(1, "fixture", "desc", "", "https://example.test", 0))
	mock.ExpectQuery(`SELECT .* FROM "friend_links" WHERE .*octet_length\("friend_links"\."tags"::text\) <= \$2`).WithArgs(1, 4096).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tags"}).AddRow(1, []byte(`["bounded"]`)))
	rows, more, err := client.ReadFriendLinkPage(t.Context(), 1, 2)
	if err != nil || more || len(rows) != 1 || len(rows[0].Tags) != 1 || rows[0].Tags[0] != "bounded" {
		t.Fatalf("page produced unexpected result: %v, more=%v, err=%v", rows, more, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
