package pgadvisory

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBeginRespondsToAcquisitionDeadline(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	mock.ExpectExec("^BEGIN$").WillDelayFor(5 * time.Second).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectClose()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if release, err := Lock(ctx, db, "SELECT lock($1)", "key"); err == nil || release != nil || ctx.Err() == nil {
		t.Fatalf("canceled BEGIN did not fail: %v", err)
	}
	if time.Since(start) > 2*time.Second || db.Stats().InUse != 0 {
		t.Fatal("timed-out BEGIN held the lock connection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReservationOutlivesAcquisitionContext(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("^BEGIN$").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT lock").WithArgs("key").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("^ROLLBACK$").WillReturnResult(sqlmock.NewResult(0, 0))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	release, err := Lock(ctx, db, "SELECT lock($1)", "key")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if db.Stats().InUse != 1 {
		t.Fatal("canceling acquisition released the reservation")
	}
	release()
	release()
	if db.Stats().InUse != 0 {
		t.Fatal("release kept a connection checked out")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
