package adminstats

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	pg "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
)

// Exercise the Ent query with the production dialect: SQLite accepts '?' and
// therefore cannot catch raw placeholders leaking into PostgreSQL queries.
func TestUploadLogFiltersPostgresParameters(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := pg.NewClient(pg.Driver(entsql.OpenDB("postgres", db)))
	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	query := `SELECT COUNT("upload_logs"."id") FROM "upload_logs" WHERE COALESCE("upload_logs"."received_at", "upload_logs"."upload_time") >= $1 AND COALESCE("upload_logs"."received_at", "upload_logs"."upload_time") < $2 AND "upload_logs"."server" IN ($3)`
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(from, to, "jp").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	count, err := applyUploadLogFilters(client.UploadLog.Query(), &uploadLogQueryFilters{From: from, To: to, Servers: []string{"jp"}}).Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count=%d", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadLogFiltersPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("HARUKI_TEST_UPLOAD_ANALYTICS_DSN")
	if dsn == "" {
		t.Skip("set HARUKI_TEST_UPLOAD_ANALYTICS_DSN for PostgreSQL integration")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TEMP TABLE upload_logs(id bigint, received_at timestamptz, upload_time timestamptz, server text)`)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	// Legacy fallback, received_at precedence, exclusive upper bound, and region.
	_, err = db.Exec(`INSERT INTO upload_logs VALUES (1,NULL,$1,'jp'),(2,$1,$2,'jp'),(3,$2,$1,'jp'),(4,NULL,$1,'en')`, from, to)
	if err != nil {
		t.Fatal(err)
	}
	client := pg.NewClient(pg.Driver(entsql.OpenDB("postgres", db)))
	n, err := applyUploadLogFilters(client.UploadLog.Query(), &uploadLogQueryFilters{From: from, To: to, Servers: []string{"jp"}}).Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count=%d, want 2", n)
	}
}
