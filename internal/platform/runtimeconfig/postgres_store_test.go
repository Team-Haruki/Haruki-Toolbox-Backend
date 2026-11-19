package runtimeconfig

import (
	"database/sql/driver"
	"errors"
	"regexp"
	"slices"
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
	store, err := NewPostgresStore(t.Context(), db, nil, initial, true)
	if err != nil {
		t.Fatal(err)
	}
	secret := "rotated-fixture"
	mock.ExpectQuery("UPDATE runtime_configurations.*jsonb_set.*_version.*RETURNING snapshot").WithArgs(1, `{"privateApiToken":"rotated-fixture"}`).
		WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow(`{"privateApiToken":"rotated-fixture","_version":1}`))
	if err := New(initial, store).Update(t.Context(), Update{PrivateAPIToken: &secret}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow(`{"privateApiToken":"rotated-fixture","_version":1}`))
	}
	store, err = NewPostgresStore(t.Context(), db, nil, initial, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(initial, store).Current(t.Context())
	if err != nil || got.PrivateAPIToken != secret || got.WebhookJWTSecret != "" || got.Version != 1 {
		t.Fatalf("unexpected durable credential state: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRuntimeStoreImportsLegacyCredentialsOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const read = `SELECT snapshot FROM runtime_configurations WHERE id = $1`
	const imported = `{"publicApiAllowedKeys":null,"privateApiToken":"rotated-token","privateApiUserAgent":"","webhookJwtSecret":"rotated-signer"}`
	legacy := &memoryStore{
		found: true,
		snapshot: Snapshot{
			PrivateAPIToken:  "rotated-token",
			WebhookJWTSecret: "rotated-signer",
		},
	}
	startup := Snapshot{PrivateAPIToken: "stale-config-token", WebhookJWTSecret: "stale-config-signer"}
	mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	if _, err := NewPostgresStore(t.Context(), db, legacy, startup, false); !errors.Is(err, ErrRuntimeConfigCutoverRequired) {
		t.Fatalf("initialized without explicit cutoff confirmation: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	mock.ExpectExec("INSERT INTO runtime_configurations").WithArgs(1, imported).WillReturnResult(sqlmock.NewResult(1, 1))
	if _, err := NewPostgresStore(t.Context(), db, legacy, startup, true); err != nil {
		t.Fatal(err)
	}

	legacy.snapshot = Snapshot{PrivateAPIToken: "stale-cache-token"}
	legacy.loadErr = errors.New("legacy Redis is unavailable")
	for range 2 {
		mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow(imported))
	}
	store, err := NewPostgresStore(t.Context(), db, legacy, startup, false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, found, err := store.Load(t.Context())
	if err != nil || !found || snapshot.PrivateAPIToken != "rotated-token" || snapshot.WebhookJWTSecret != "rotated-signer" {
		t.Fatalf("durable store did not preserve imported settings: found=%v err=%v", found, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRuntimeStoreFailsClosedWhenDurableRowDisappears(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const read = `SELECT snapshot FROM runtime_configurations WHERE id = $1`
	mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	store := &postgresStore{db: db}
	if _, found, err := store.Load(t.Context()); found || !errors.Is(err, errMissingRuntimeConfiguration) {
		t.Fatalf("missing durable row: found=%v err=%v", found, err)
	}
	enabled := false
	mock.ExpectQuery("UPDATE runtime_configurations").WithArgs(1, `{"webhookEnabled":false}`).
		WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	if _, err := store.Apply(t.Context(), Update{WebhookEnabled: &enabled}, Snapshot{}); !errors.Is(err, errMissingRuntimeConfiguration) {
		t.Fatalf("update on missing durable row: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRuntimeStorePreservesDependencyErrorsAndAtomicUpdates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const read = `SELECT snapshot FROM runtime_configurations WHERE id = $1`
	outage := errors.New("fixture settings storage unavailable")
	mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnError(outage)
	if _, err := NewPostgresStore(t.Context(), db, nil, Snapshot{}, false); !errors.Is(err, outage) {
		t.Fatalf("startup did not surface database outage: %v", err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(read)).WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	if _, err := NewPostgresStore(t.Context(), db, &memoryStore{loadErr: outage}, Snapshot{}, true); !errors.Is(err, outage) {
		t.Fatalf("startup did not surface legacy storage outage: %v", err)
	}

	store := &postgresStore{db: db}
	keys, enabled := []string{"userCards"}, true
	update := Update{AllowedKeys: &keys, OAuth2DeviceFlowEnabled: &enabled}
	match := &runtimePatchMatch{wantKeys: keys[0]}
	mock.ExpectQuery("UPDATE runtime_configurations").WithArgs(1, match).WillReturnError(outage)
	if _, err := store.Apply(t.Context(), update, Snapshot{}); !errors.Is(err, outage) {
		t.Fatalf("settings write did not surface database outage: %v", err)
	}
	mock.ExpectQuery("UPDATE runtime_configurations").WithArgs(1, match).
		WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow(`{"publicApiAllowedKeys":["userCards"],"oauth2DeviceFlowEnabled":true,"privateApiToken":"existing-fixture"}`))
	got, err := store.Apply(t.Context(), update, Snapshot{})
	if err != nil || !got.OAuth2DeviceFlowSwitchOn() || !slices.Equal(got.AllowedKeys, keys) || got.PrivateAPIToken != "existing-fixture" {
		t.Fatalf("partial update did not return the committed settings: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type runtimePatchMatch struct{ wantKeys string }

func (m *runtimePatchMatch) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var patch Snapshot
	return unmarshalSnapshot([]byte(raw), &patch) == nil && patch.OAuth2DeviceFlowSwitchOn() &&
		slices.Equal(patch.AllowedKeys, []string{m.wantKeys})
}
