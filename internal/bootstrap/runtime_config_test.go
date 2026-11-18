package bootstrap

import (
	"database/sql"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"reflect"
	"sort"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	platformRuntimeConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	_ "github.com/mattn/go-sqlite3"
)

func TestEntRuntimeConfigurationOnlyStoresObjects(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	client := postgresql.NewClient(postgresql.Driver(entsql.OpenDB(dialect.SQLite, db)))
	defer client.Close()
	if err := client.Schema.Create(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`[]`, `"{}"`, `null`, `false`} {
		_, err := db.ExecContext(t.Context(), "INSERT INTO runtime_configurations (id, snapshot) VALUES (1, ?)", invalid)
		if err == nil {
			t.Fatalf("database accepted non-object runtime settings: %s", invalid)
		}
	}
	row, err := client.RuntimeConfiguration.Create().SetSnapshot(map[string]any{"webhookEnabled": false}).Save(t.Context())
	if err != nil || row.Snapshot["webhookEnabled"] != false {
		t.Fatalf("object did not round-trip through Ent: %v", err)
	}
}

func TestRuntimeConfigCompositionCopiesMutableValues(t *testing.T) {
	cfg := harukiConfig.Config{}
	cfg.Others.AllowedKeys = []string{"key-a", "key-b"}
	cfg.MongoDB.PrivateApiSecret = "private-token"
	cfg.MongoDB.PrivateApiUserAgent = "private-agent"
	cfg.Webhook.JWTSecret = "webhook-secret"
	cfg.Webhook.Enabled = true

	service := newRuntimeConfigService(cfg, nil)
	cfg.Others.AllowedKeys[0] = "mutated-after-build"
	cfg.Webhook.Enabled = false

	snapshot, err := service.Current(t.Context())
	if err != nil {
		t.Fatalf("Current returned error: %v", err)
	}
	if !reflect.DeepEqual(snapshot.AllowedKeys, []string{"key-a", "key-b"}) {
		t.Fatalf("AllowedKeys = %#v, want copied startup values", snapshot.AllowedKeys)
	}
	if snapshot.WebhookEnabled == nil || !*snapshot.WebhookEnabled {
		t.Fatalf("WebhookEnabled = %v, want independent true pointer", snapshot.WebhookEnabled)
	}

	snapshot.AllowedKeys[0] = "mutated-return-value"
	*snapshot.WebhookEnabled = false
	again, err := service.Current(t.Context())
	if err != nil {
		t.Fatalf("second Current returned error: %v", err)
	}
	if again.AllowedKeys[0] != "key-a" || again.WebhookEnabled == nil || !*again.WebhookEnabled {
		t.Fatalf("service leaked mutable snapshot values: %#v", again)
	}
}

func TestRuntimeConfigRedisContractRemainsStable(t *testing.T) {
	cfg := harukiConfig.Config{}
	cfg.Webhook.Enabled = true
	payload, err := json.Marshal(runtimeConfigSnapshotFromConfig(cfg))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var fields map[string]jsontext.Value
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	gotFields := make([]string, 0, len(fields))
	for field := range fields {
		gotFields = append(gotFields, field)
	}
	sort.Strings(gotFields)
	// The shared-key HarukiProxy fields (harukiProxySecret, harukiProxyUnpackKey,
	// harukiProxyUserAgent, harukiProxyVersion) were removed with that upload
	// channel; a stored snapshot still holding them is read without them and
	// rewritten without them on the next runtime config update.
	wantFields := []string{
		"privateApiToken",
		"privateApiUserAgent",
		"publicApiAllowedKeys",
		"webhookEnabled",
		"webhookJwtSecret",
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("runtime config JSON fields = %#v, want %#v", gotFields, wantFields)
	}
	if got := harukiRedis.BuildRuntimeConfigKey(); got != "haruki:config:runtime" {
		t.Fatalf("runtime config Redis key = %q, want %q", got, "haruki:config:runtime")
	}
}

func TestDurableRuntimeConfigCompositionFailsClosedAndReadsPostgres(t *testing.T) {
	t.Setenv("HARUKI_RUNTIME_CONFIG_CUTOVER", "0")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := harukiConfig.Config{}
	cfg.MongoDB.PrivateApiSecret = "startup-fixture"
	cfg.Webhook.JWTSecret = "startup-signer"
	outage := errors.New("fixture database unavailable")
	mock.ExpectQuery("SELECT snapshot FROM runtime_configurations").WithArgs(1).WillReturnError(outage)
	if service, err := newDurableRuntimeConfigService(t.Context(), cfg, db, nil); service != nil || !errors.Is(err, outage) {
		t.Fatalf("configuration startup did not reject database failure: %v", err)
	}
	mock.ExpectQuery("SELECT snapshot FROM runtime_configurations").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	if service, err := newDurableRuntimeConfigService(t.Context(), cfg, db, nil); service != nil || !errors.Is(err, platformRuntimeConfig.ErrRuntimeConfigCutoverRequired) {
		t.Fatalf("settings import bypassed cutover confirmation: %v", err)
	}
	t.Setenv("HARUKI_RUNTIME_CONFIG_CUTOVER", "1")
	mock.ExpectQuery("SELECT snapshot FROM runtime_configurations").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"snapshot"}))
	mock.ExpectExec("INSERT INTO runtime_configurations").WithArgs(1, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	if _, err := newDurableRuntimeConfigService(t.Context(), cfg, db, nil); err != nil {
		t.Fatalf("confirmed settings import failed: %v", err)
	}
	t.Setenv("HARUKI_RUNTIME_CONFIG_CUTOVER", "0")
	for range 2 {
		mock.ExpectQuery("SELECT snapshot FROM runtime_configurations").WithArgs(1).
			WillReturnRows(sqlmock.NewRows([]string{"snapshot"}).AddRow(`{"privateApiToken":"durable-fixture","publicApiAllowedKeys":["userCards"]}`))
	}
	service, err := newDurableRuntimeConfigService(t.Context(), cfg, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Current(t.Context())
	if err != nil || got.PrivateAPIToken != "durable-fixture" || got.WebhookJWTSecret != "" ||
		!reflect.DeepEqual(got.AllowedKeys, []string{"userCards"}) {
		t.Fatalf("service did not use authoritative settings: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
