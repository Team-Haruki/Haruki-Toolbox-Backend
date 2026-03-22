package bootstrap

import (
	"context"
	harukiConfig "haruki-suite/config"
	dbManager "haruki-suite/utils/database/postgresql"
	harukiRedis "haruki-suite/utils/database/redis"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestOpenMainLogWriterStdout(t *testing.T) {
	writer, cleanup, err := openMainLogWriter("")
	if err != nil {
		t.Fatalf("openMainLogWriter returned error: %v", err)
	}
	if writer == nil {
		t.Fatalf("writer is nil")
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}
}

func TestOpenMainLogWriterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.log")
	writer, cleanup, err := openMainLogWriter(path)
	if err != nil {
		t.Fatalf("openMainLogWriter returned error: %v", err)
	}
	defer func() {
		if closeErr := cleanup(); closeErr != nil {
			t.Fatalf("cleanup returned error: %v", closeErr)
		}
	}()

	if writer == nil {
		t.Fatalf("writer is nil")
	}
	if _, err := io.WriteString(writer, "hello\n"); err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) == "" {
		t.Fatalf("log file content is empty")
	}
}

func TestEnsureRedisReadyNilManager(t *testing.T) {
	if err := ensureRedisReady(context.Background(), nil); err == nil {
		t.Fatalf("ensureRedisReady should fail for nil manager")
	}
}

func TestEnsureRedisReadyPingFailure(t *testing.T) {
	t.Parallel()

	manager := &harukiRedis.HarukiRedisManager{
		Redis: goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"}),
	}
	defer func() {
		_ = manager.Redis.Close()
	}()

	err := ensureRedisReady(context.Background(), manager)
	if err == nil {
		t.Fatalf("ensureRedisReady should fail when ping fails")
	}
	if !strings.Contains(err.Error(), "redis ping failed") {
		t.Fatalf("error = %v, want redis ping failed", err)
	}
}

func TestEnsureRedisReadySuccess(t *testing.T) {
	t.Parallel()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	defer srv.Close()

	manager := &harukiRedis.HarukiRedisManager{
		Redis: goredis.NewClient(&goredis.Options{Addr: srv.Addr()}),
	}
	defer func() {
		_ = manager.Redis.Close()
	}()

	if err := ensureRedisReady(context.Background(), manager); err != nil {
		t.Fatalf("ensureRedisReady returned error: %v", err)
	}
}

func newBootstrapSQLMockClient(t *testing.T) (*dbManager.Client, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	client := dbManager.NewClient(dbManager.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client, mock
}

func expectSchemaExistsQuery(mock sqlmock.Sqlmock, query string, exists bool) {
	rows := sqlmock.NewRows([]string{"exists"}).AddRow(exists)
	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnRows(rows)
}

func TestEnsureUsersSchemaCompatibilityValidatesWithoutDDLWhenAutoMigrateDisabled(t *testing.T) {
	client, mock := newBootstrapSQLMockClient(t)

	expectSchemaExistsQuery(mock, checkUsersEmailLowerUniqueIndexExistsSQL, true)
	expectSchemaExistsQuery(mock, checkUsersKratosIdentityColumnExistsSQL, true)
	expectSchemaExistsQuery(mock, checkUsersKratosIdentityUniqueIndexExistsSQL, true)

	if err := ensureUsersSchemaCompatibility(context.Background(), client, false); err != nil {
		t.Fatalf("ensureUsersSchemaCompatibility returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestEnsureUsersSchemaCompatibilityFailsWhenManualSchemaIsIncomplete(t *testing.T) {
	client, mock := newBootstrapSQLMockClient(t)

	expectSchemaExistsQuery(mock, checkUsersEmailLowerUniqueIndexExistsSQL, true)
	expectSchemaExistsQuery(mock, checkUsersKratosIdentityColumnExistsSQL, false)

	err := ensureUsersSchemaCompatibility(context.Background(), client, false)
	if err == nil {
		t.Fatalf("expected ensureUsersSchemaCompatibility to fail for missing kratos column")
	}
	if !strings.Contains(err.Error(), "users.kratos_identity_id column is missing") {
		t.Fatalf("error = %v, want missing kratos_identity_id column", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestEnsureUsersSchemaCompatibilityCreatesDDLWhenAutoMigrateEnabled(t *testing.T) {
	client, mock := newBootstrapSQLMockClient(t)

	duplicateRows := sqlmock.NewRows([]string{"normalized_email", "cnt"})
	mock.ExpectQuery(regexp.QuoteMeta(findUsersEmailLowerDuplicateSQL)).WillReturnRows(duplicateRows)
	mock.ExpectExec(regexp.QuoteMeta(createUsersEmailLowerUniqueIndexSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(createUsersKratosIdentityColumnSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(createUsersKratosIdentityUniqueIndexSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := ensureUsersSchemaCompatibility(context.Background(), client, true); err != nil {
		t.Fatalf("ensureUsersSchemaCompatibility returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestEnsureWebhookSchemaCompatibilityValidatesWithoutDDLWhenAutoMigrateDisabled(t *testing.T) {
	client, mock := newBootstrapSQLMockClient(t)

	rows := sqlmock.NewRows([]string{"to_regclass"}).AddRow("webhook_endpoints")
	mock.ExpectQuery(regexp.QuoteMeta(checkWebhookEndpointsTableExistsSQL)).WillReturnRows(rows)
	rows = sqlmock.NewRows([]string{"to_regclass"}).AddRow("webhook_subscriptions")
	mock.ExpectQuery(regexp.QuoteMeta(checkWebhookSubscriptionsTableExistsSQL)).WillReturnRows(rows)
	expectSchemaExistsQuery(mock, checkWebhookEndpointsEnabledColumnExistsSQL, true)

	if err := ensureWebhookSchemaCompatibility(context.Background(), client, false); err != nil {
		t.Fatalf("ensureWebhookSchemaCompatibility returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestEnsureWebhookSchemaCompatibilityFailsWhenManualSchemaIsIncomplete(t *testing.T) {
	client, mock := newBootstrapSQLMockClient(t)

	rows := sqlmock.NewRows([]string{"to_regclass"}).AddRow(nil)
	mock.ExpectQuery(regexp.QuoteMeta(checkWebhookEndpointsTableExistsSQL)).WillReturnRows(rows)

	err := ensureWebhookSchemaCompatibility(context.Background(), client, false)
	if err == nil {
		t.Fatalf("expected ensureWebhookSchemaCompatibility to fail for missing table")
	}
	if !strings.Contains(err.Error(), "webhook_endpoints table is missing") {
		t.Fatalf("error = %v, want missing webhook_endpoints table", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestEnsureWebhookSchemaCompatibilityFailsWhenEnabledColumnMissing(t *testing.T) {
	client, mock := newBootstrapSQLMockClient(t)

	rows := sqlmock.NewRows([]string{"to_regclass"}).AddRow("webhook_endpoints")
	mock.ExpectQuery(regexp.QuoteMeta(checkWebhookEndpointsTableExistsSQL)).WillReturnRows(rows)
	rows = sqlmock.NewRows([]string{"to_regclass"}).AddRow("webhook_subscriptions")
	mock.ExpectQuery(regexp.QuoteMeta(checkWebhookSubscriptionsTableExistsSQL)).WillReturnRows(rows)
	expectSchemaExistsQuery(mock, checkWebhookEndpointsEnabledColumnExistsSQL, false)

	err := ensureWebhookSchemaCompatibility(context.Background(), client, false)
	if err == nil {
		t.Fatalf("expected ensureWebhookSchemaCompatibility to fail for missing enabled column")
	}
	if !strings.Contains(err.Error(), "webhook_endpoints.enabled column is missing") {
		t.Fatalf("error = %v, want missing webhook_endpoints.enabled column", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestValidateOAuth2ProviderConfig(t *testing.T) {
	t.Run("hydra requires public and admin urls", func(t *testing.T) {
		cfg := harukiConfig.Config{}
		cfg.OAuth2.Provider = "hydra"
		if err := validateOAuth2ProviderConfig(cfg); err == nil {
			t.Fatalf("expected missing hydra urls to fail")
		}
		cfg.OAuth2.HydraPublicURL = "https://hydra-public.example.com"
		if err := validateOAuth2ProviderConfig(cfg); err == nil {
			t.Fatalf("expected missing admin url to fail")
		}
		cfg.OAuth2.HydraAdminURL = "https://hydra-admin.example.com"
		if err := validateOAuth2ProviderConfig(cfg); err != nil {
			t.Fatalf("expected complete hydra config to pass, got %v", err)
		}
	})

	t.Run("builtin provider rejected", func(t *testing.T) {
		cfg := harukiConfig.Config{}
		cfg.OAuth2.Provider = "builtin"
		if err := validateOAuth2ProviderConfig(cfg); err == nil {
			t.Fatalf("expected builtin provider to be rejected")
		}
	})
}

func TestValidateUserSystemConfig(t *testing.T) {
	t.Run("auth proxy requires session header", func(t *testing.T) {
		cfg := harukiConfig.Config{}
		cfg.UserSystem.AuthProvider = "kratos"
		cfg.UserSystem.KratosPublicURL = "https://kratos-public.example.com"
		cfg.UserSystem.KratosAdminURL = "https://kratos-admin.example.com"
		cfg.UserSystem.AuthProxyEnabled = true
		cfg.UserSystem.AuthProxyTrustedHeader = "X-Auth-Proxy-Secret"
		cfg.UserSystem.AuthProxyTrustedValue = "shared-secret"
		cfg.UserSystem.AuthProxySubjectHeader = "X-Kratos-Identity-Id"
		if err := validateUserSystemConfig(cfg); err == nil {
			t.Fatalf("expected missing auth proxy session header to fail")
		}
		cfg.UserSystem.AuthProxySessionHeader = "X-Auth-Proxy-Session-Id"
		if err := validateUserSystemConfig(cfg); err != nil {
			t.Fatalf("expected complete auth proxy config to pass, got %v", err)
		}
	})
}
