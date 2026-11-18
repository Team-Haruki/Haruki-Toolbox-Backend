package runtimeconfig

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDurableRuntimeSettingsSurviveCacheLossAndRestart(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const read = `SELECT snapshot FROM runtime_configurations WHERE id = $1`
	mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	seed := `{"publicApiAllowedKeys":null,"privateApiToken":"","privateApiUserAgent":"","webhookJwtSecret":""}`
	mock.ExpectExec("INSERT INTO runtime_configurations").WithArgs(1, seed).WillReturnResult(sqlmock.NewResult(1, 1))
	initial := Snapshot{PrivateAPIToken: "retired-fixture", WebhookJWTSecret: "retired-signer"}
	store, err := NewPostgresStore(t.Context(), db, nil, initial)
	if err != nil {
		t.Fatal(err)
	}
	secret := "rotated-fixture"
	mock.ExpectQuery("UPDATE runtime_configurations").WithArgs(1, `{"privateApiToken":"rotated-fixture"}`).
		WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow(`{"privateApiToken":"rotated-fixture"}`))
	if err := New(initial, store).Update(t.Context(), Update{PrivateAPIToken: &secret}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow(`{"privateApiToken":"rotated-fixture"}`))
	}
	store, err = NewPostgresStore(t.Context(), db, nil, initial)
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(initial, store).Current(t.Context())
	if err != nil || got.PrivateAPIToken != secret || got.WebhookJWTSecret != "" {
		t.Fatalf("unexpected durable credential state: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
