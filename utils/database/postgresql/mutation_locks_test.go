package postgresql_test

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	_ "github.com/mattn/go-sqlite3"
)

func TestPostgresMutationReservationLocksInStableOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	client := postgresql.NewClient(postgresql.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	query := regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtextextended($1, 452731))`)
	mock.ExpectExec("^BEGIN$").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(query).WithArgs("game:jp:123").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(query).WithArgs("user:a").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(query).WithArgs("user:z").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("^ROLLBACK$").WillReturnResult(sqlmock.NewResult(0, 0))
	release, err := client.LockGameAccountAndUserMutations(t.Context(), "jp", "123", "z", "a", "a")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGrantRevocationWaitsForReservedUploadCommit(t *testing.T) {
	ctx := t.Context()
	db := enttest.Open(t, "sqlite3", "file:grant-mutation-reservation?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range []string{"owner", "delegate"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").SaveX(ctx)
	}
	db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetUserID("owner").SetVerified(true).SaveX(ctx)
	if _, err := db.UpsertGameAccountDataGrant(ctx, "owner", "delegate", "jp", "123", "suite", time.Now().Add(time.Hour), []string{"write"}); err != nil {
		t.Fatal(err)
	}
	release, err := db.LockGameAccountMutation(ctx, "jp", "123")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if n, err := db.DeleteGameAccountDataGrant(blocked, "owner", "delegate", "jp", "123", "suite"); n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("revocation during reservation = %d, %v", n, err)
	}
	release()
	if n, err := db.DeleteGameAccountDataGrant(ctx, "owner", "delegate", "jp", "123", "suite"); n != 1 || err != nil {
		t.Fatalf("revocation after release = %d, %v", n, err)
	}
	access, err := db.CanWriteGameAccountData(ctx, "delegate", "jp", "123", "suite", time.Now())
	if err != nil || access.Allowed {
		t.Fatalf("authorization after revocation = %+v, %v", access, err)
	}
}
