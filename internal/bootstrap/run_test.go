package bootstrap

import (
	"context"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

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

func TestPrepareToolboxDatabaseValidatesAndCleansUpManualSchema(t *testing.T) {
	client, mock := newBootstrapSQLMockClient(t)
	cfg := harukiConfig.Config{}
	logger := harukiLogger.NewLogger("BootstrapTest", "INFO", io.Discard)

	rows := sqlmock.NewRows([]string{"to_regclass"}).AddRow("users")
	mock.ExpectQuery(regexp.QuoteMeta(checkUsersTableExistsSQL)).WillReturnRows(rows)
	expectSchemaExistsQuery(mock, checkUsersEmailLowerUniqueIndexExistsSQL, true)
	expectSchemaExistsQuery(mock, checkUsersKratosIdentityColumnExistsSQL, true)
	expectSchemaExistsQuery(mock, checkUsersKratosIdentityUniqueIndexExistsSQL, true)
	rows = sqlmock.NewRows([]string{"to_regclass"}).AddRow("webhook_endpoints")
	mock.ExpectQuery(regexp.QuoteMeta(checkWebhookEndpointsTableExistsSQL)).WillReturnRows(rows)
	rows = sqlmock.NewRows([]string{"to_regclass"}).AddRow("webhook_subscriptions")
	mock.ExpectQuery(regexp.QuoteMeta(checkWebhookSubscriptionsTableExistsSQL)).WillReturnRows(rows)
	expectSchemaExistsQuery(mock, checkWebhookEndpointsEnabledColumnExistsSQL, true)
	uploadRows := sqlmock.NewRows([]string{"column_name", "is_nullable"})
	for _, column := range uploadlog.Columns {
		uploadRows.AddRow(column, "YES")
	}
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM information_schema.columns").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(regexp.QuoteMeta(uploadLogColumnsSQL)).WillReturnRows(uploadRows)
	mock.ExpectQuery(regexp.QuoteMeta(checkSponsorDurationSchemaSQL)).WillReturnRows(sqlmock.NewRows([]string{"ok"}).AddRow(true))
	mock.ExpectExec("DELETE FROM").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 2))

	if err := prepareToolboxDatabase(cfg, client, logger); err != nil {
		t.Fatalf("prepareToolboxDatabase returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
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
		cfg.UserSystem.AuthProxyTrustedValue = "a-sufficiently-long-shared-secret"
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

func TestValidateBackendConfigRequiresExactTrustedProxyAddresses(t *testing.T) {
	t.Run("trust proxy disabled ignores list", func(t *testing.T) {
		cfg := harukiConfig.Config{}
		cfg.Backend.TrustProxies = []string{"10.0.0.0/8"}
		if err := validateBackendConfig(cfg); err != nil {
			t.Fatalf("disabled trust proxy should ignore list: %v", err)
		}
	})

	t.Run("broad networks rejected", func(t *testing.T) {
		cfg := harukiConfig.Config{}
		cfg.Backend.EnableTrustProxy = true
		cfg.Backend.ProxyHeader = "X-Forwarded-For"
		cfg.Backend.TrustProxies = []string{"100.64.0.0/10"}
		if err := validateBackendConfig(cfg); err == nil {
			t.Fatal("expected a shared trusted proxy network to be rejected")
		}
	})

	t.Run("exact edge addresses accepted", func(t *testing.T) {
		cfg := harukiConfig.Config{}
		cfg.Backend.EnableTrustProxy = true
		cfg.Backend.ProxyHeader = "X-Forwarded-For"
		cfg.Backend.TrustProxies = []string{"127.0.0.1", "10.42.0.7/32", "::1/128"}
		if err := validateBackendConfig(cfg); err != nil {
			t.Fatalf("exact edge proxy addresses should pass: %v", err)
		}
	})
}

func validDeviceFlowTestConfig(t *testing.T) harukiConfig.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	// Loading an empty file yields the shipped defaults of oauth2.device_flow.
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := harukiConfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.OAuth2.HydraPublicURL = "http://hydra:4444"
	cfg.OAuth2.HydraAdminURL = "http://hydra:4445"
	cfg.OAuth2.HydraBrowserURL = "https://toolbox-api-direct.haruki.seiunx.com"
	cfg.UserSystem.FrontendURL = "https://haruki.seiunx.com"
	cfg.UserSystem.SessionSignToken = "0123456789abcdef0123456789abcdef"
	cfg.Redis.Host = "redis"
	cfg.OAuth2.DeviceFlow.Enabled = true
	return cfg
}

func TestValidateOAuth2DeviceFlowConfigSkippedWhenDisabled(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	cfg.OAuth2.DeviceFlow.Enabled = false
	// Production has no session_sign_token today; a disabled flow must not care.
	cfg.UserSystem.SessionSignToken = ""
	cfg.OAuth2.DeviceFlow.UserCodeTTL = "not a duration"
	cfg.OAuth2.DeviceFlow.Limits.LookupFailGlobalPer10m = 0
	if err := validateOAuth2DeviceFlowConfig(cfg); err != nil {
		t.Fatalf("disabled device flow was validated: %v", err)
	}
}

func TestValidateOAuth2DeviceFlowConfigAcceptsDefaults(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	if err := validateOAuth2DeviceFlowConfig(cfg); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	ttl, _ := time.ParseDuration(cfg.OAuth2.DeviceFlow.UserCodeTTL)
	if budget := deviceFlowBruteForceBudget(cfg.OAuth2.DeviceFlow, ttl); budget < 0.73 || budget > 0.75 {
		t.Fatalf("default budget = %.4f, want about 0.74", budget)
	}
	// Local development over plain http is allowed for localhost only.
	cfg.OAuth2.HydraBrowserURL = "http://localhost:4444"
	cfg.UserSystem.FrontendURL = "http://127.0.0.1:5173"
	if err := validateOAuth2DeviceFlowConfig(cfg); err != nil {
		t.Fatalf("localhost http rejected: %v", err)
	}
}

func TestValidateOAuth2DeviceFlowConfigRejects(t *testing.T) {
	cases := []struct {
		name string
		edit func(cfg *harukiConfig.Config)
		want string
	}{
		{"empty session_sign_token", func(c *harukiConfig.Config) { c.UserSystem.SessionSignToken = "" }, "session_sign_token"},
		{"short session_sign_token", func(c *harukiConfig.Config) { c.UserSystem.SessionSignToken = "   short-secret   " }, "session_sign_token"},
		{"no browser URL", func(c *harukiConfig.Config) { c.OAuth2.HydraBrowserURL = "" }, "hydra_browser_url"},
		{"no Redis", func(c *harukiConfig.Config) { c.Redis.Host = "" }, "redis"},
		{"http issuer", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.HydraIssuerURL = "http://api.example.com" }, "hydra_issuer_url"},
		{"relative verification URL", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.VerificationURL = "/device" }, "verification_url"},
		{"no frontend for the default verification URL", func(c *harukiConfig.Config) { c.UserSystem.FrontendURL = "" }, "verification_url"},
		{"origin with a path", func(c *harukiConfig.Config) {
			c.OAuth2.DeviceFlow.AllowedOrigins = []string{"https://haruki.seiunx.com/device"}
		}, "allowed_origins"},
		{"charset too short", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeCharset = "BCDFGHJ" }, "user_code_charset"},
		{"lower-case charset", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeCharset = "bcdfghjklm" }, "user_code_charset"},
		{"repeated charset", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeCharset = "BCDFGHJKLB" }, "user_code_charset"},
		{"length too short", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeLength = 5 }, "user_code_length"},
		{"length too long", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeLength = 13 }, "user_code_length"},
		{"zero limit", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.Limits.DecisionUserPerDay = 0 }, "decision_user_per_day"},
		{"pools exceed global", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.Limits.AuthIssuedPublicPer10m = 700 }, "auth_issued_global_per_10m"},
		{"lease too short", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.ApproveLeaseSeconds = 25 }, "approve_lease_seconds"},
		{"remaining below timeout", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.MinRemainingSecondsToApprove = 10 }, "min_remaining_seconds_to_approve"},
		{"unparsable TTL", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeTTL = "ten minutes" }, "user_code_ttl"},
		{"TTL too long", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeTTL = "31m" }, "user_code_ttl"},
		// Default limits with a TTL over 10 minutes add a live window and
		// break the budget (about 1.11 guesses per year at 20 minutes).
		{"budget exceeded by TTL", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeTTL = "20m" }, "guesses per year"},
		{"budget exceeded by lookup failures", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.Limits.LookupFailGlobalPer10m = 300 }, "guesses per year"},
		{"budget exceeded by a short code", func(c *harukiConfig.Config) { c.OAuth2.DeviceFlow.UserCodeLength = 7 }, "guesses per year"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validDeviceFlowTestConfig(t)
			tc.edit(&cfg)
			err := validateOAuth2DeviceFlowConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestBuildRefusesInvalidEnabledDeviceFlowBeforeAcquiringResources(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	cfg.UserSystem.KratosPublicURL = "http://kratos:4433"
	cfg.UserSystem.KratosAdminURL = "http://kratos:4434"
	cfg.GameData.URL = "postgres://unused"
	cfg.UserSystem.SessionSignToken = ""
	if _, err := Build(cfg); err == nil || !strings.Contains(err.Error(), "session_sign_token") {
		t.Fatalf("Build err = %v, want the device flow validation error", err)
	}
}

func TestNewOAuth2DeviceFlowConfigResolvesDefaults(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	flow := newOAuth2DeviceFlowConfig(cfg, nil)
	if !flow.Enabled() || flow.VerificationURL() != "https://haruki.seiunx.com/device" ||
		flow.HydraIssuerOrigin() != "https://toolbox-api-direct.haruki.seiunx.com" ||
		!flow.OriginAllowed("https://haruki.seiunx.com") || flow.UserCodeTTL() != 10*time.Minute ||
		flow.UserCodeLength() != 8 || flow.UserCodeCharset() != "BCDFGHJKLMNPQRSTVWXZ" ||
		flow.Timings().ReaperInterval != time.Minute || flow.Limits().AuthIssuedGlobalPer10m != 1200 {
		t.Fatalf("resolved device flow config is wrong: %+v", flow)
	}
}
