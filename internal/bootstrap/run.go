package bootstrap

import (
	"context"
	"crypto/rand"
	stdsql "database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	harukiAPI "haruki-suite/api"
	harukiConfig "haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiDatabaseManager "haruki-suite/utils/database"
	harukiMongo "haruki-suite/utils/database/mongo"
	dbManager "haruki-suite/utils/database/postgresql"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiHandler "haruki-suite/utils/handler"
	harukiLogger "haruki-suite/utils/logger"
	harukiSekaiAPIClient "haruki-suite/utils/sekaiapi"
	harukiSMTP "haruki-suite/utils/smtp"
	harukiVersion "haruki-suite/version"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	_ "github.com/lib/pq"
)

const (
	checkUsersTableExistsSQL = `SELECT to_regclass('public.users')`

	createUsersEmailLowerUniqueIndexSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_unique_idx
ON users (LOWER(email));
`
	findUsersEmailLowerDuplicateSQL = `
SELECT LOWER(email) AS normalized_email, COUNT(*) AS cnt
FROM users
GROUP BY LOWER(email)
HAVING COUNT(*) > 1
LIMIT 1;
`
	createUsersKratosIdentityColumnSQL = `
ALTER TABLE users
ADD COLUMN IF NOT EXISTS kratos_identity_id TEXT;
`
	createUsersKratosIdentityUniqueIndexSQL = `
CREATE UNIQUE INDEX IF NOT EXISTS users_kratos_identity_id_unique_idx
ON users (kratos_identity_id);
`
)

func openMainLogWriter(mainLogPath string) (io.Writer, func() error, error) {
	if strings.TrimSpace(mainLogPath) == "" {
		return os.Stdout, func() error { return nil }, nil
	}

	logFile, err := os.OpenFile(mainLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, err
	}

	writer := io.MultiWriter(os.Stdout, logFile)
	cleanup := func() error {
		return logFile.Close()
	}
	return writer, cleanup, nil
}

func ensureUsersEmailLowerUniqueIndex(ctx context.Context, entClient *dbManager.Client) error {
	sqlDB := entClient.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("underlying SQL DB is not available")
	}

	var normalizedEmail string
	var duplicateCount int
	err := sqlDB.QueryRowContext(ctx, findUsersEmailLowerDuplicateSQL).Scan(&normalizedEmail, &duplicateCount)
	if err != nil && !errors.Is(err, stdsql.ErrNoRows) {
		return fmt.Errorf("query case-insensitive email duplicates: %w", err)
	}
	if err == nil {
		return fmt.Errorf("case-insensitive duplicate emails exist (normalized=%q, count=%d)", normalizedEmail, duplicateCount)
	}

	if _, err := sqlDB.ExecContext(ctx, createUsersEmailLowerUniqueIndexSQL); err != nil {
		return fmt.Errorf("create users lower(email) unique index: %w", err)
	}
	return nil
}

func ensureUsersKratosIdentityColumn(ctx context.Context, entClient *dbManager.Client) error {
	sqlDB := entClient.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("underlying SQL DB is not available")
	}

	if _, err := sqlDB.ExecContext(ctx, createUsersKratosIdentityColumnSQL); err != nil {
		return fmt.Errorf("add users.kratos_identity_id column: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, createUsersKratosIdentityUniqueIndexSQL); err != nil {
		return fmt.Errorf("create users.kratos_identity_id unique index: %w", err)
	}
	return nil
}

func usersTableExists(ctx context.Context, entClient *dbManager.Client) (bool, error) {
	sqlDB := entClient.SQLDB()
	if sqlDB == nil {
		return false, fmt.Errorf("underlying SQL DB is not available")
	}

	var regclassName stdsql.NullString
	if err := sqlDB.QueryRowContext(ctx, checkUsersTableExistsSQL).Scan(&regclassName); err != nil {
		return false, fmt.Errorf("check users table existence: %w", err)
	}
	return regclassName.Valid && strings.TrimSpace(regclassName.String) != "", nil
}

func Run(cfg harukiConfig.Config) error {
	loggerWriter, closeMainLogFile, err := openMainLogWriter(cfg.Backend.MainLogFile)
	if err != nil {
		return fmt.Errorf("open main log file: %w", err)
	}
	defer func() {
		_ = closeMainLogFile()
	}()

	mainLogger := harukiLogger.NewLogger("Main", cfg.Backend.LogLevel, loggerWriter)
	mainLogger.Infof("%s", fmt.Sprintf("========================= Haruki Toolbox Backend %s =========================", harukiVersion.Version))
	mainLogger.Infof("Powered By Haruki Dev Team")

	sekaiAPIClient := harukiSekaiAPIClient.NewHarukiSekaiAPIClient(cfg.SekaiAPI.APIEndpoint, cfg.SekaiAPI.APIToken)
	mongoManager, err := harukiMongo.NewMongoDBManager(
		context.Background(),
		cfg.MongoDB.URL,
		cfg.MongoDB.DB,
		cfg.MongoDB.Suite,
		cfg.MongoDB.Mysekai,
		cfg.MongoDB.Webhook,
		cfg.MongoDB.WebhookUser,
	)
	if err != nil {
		return fmt.Errorf("init MongoDB: %w", err)
	}

	redisClient := harukiRedis.NewRedisClient(cfg.Redis)
	entClient, err := dbManager.Open(cfg.UserSystem.DBType, cfg.UserSystem.DBURL)
	if err != nil {
		return fmt.Errorf("init PostgreSQL: %w", err)
	}
	defer func() {
		_ = entClient.Close()
	}()
	if cfg.Backend.AutoMigrate {
		if err := entClient.Schema.Create(context.Background()); err != nil {
			return fmt.Errorf("create schema resources: %w", err)
		}
		mainLogger.Infof("auto schema migration completed")
	} else {
		mainLogger.Infof("auto schema migration disabled")
		exists, existsErr := usersTableExists(context.Background(), entClient)
		if existsErr != nil {
			return fmt.Errorf("check schema state when auto_migrate disabled: %w", existsErr)
		}
		if !exists {
			return fmt.Errorf("database schema is not initialized (users table missing) while backend.auto_migrate=false")
		}
	}
	if err := ensureUsersEmailLowerUniqueIndex(context.Background(), entClient); err != nil {
		return fmt.Errorf("ensure case-insensitive email uniqueness: %w", err)
	}
	if err := ensureUsersKratosIdentityColumn(context.Background(), entClient); err != nil {
		return fmt.Errorf("ensure kratos identity mapping column: %w", err)
	}

	smtpClient := harukiSMTP.NewSMTPClient(cfg.UserSystem.SMTP)
	sessionHandler := harukiAPIHelper.NewSessionHandler(redisClient.Redis, cfg.UserSystem.SessionSignToken)
	sessionHandler.ConfigureIdentityProvider(
		cfg.UserSystem.AuthProvider,
		cfg.UserSystem.KratosPublicURL,
		cfg.UserSystem.KratosAdminURL,
		cfg.UserSystem.KratosSessionHeader,
		cfg.UserSystem.KratosSessionCookie,
		cfg.UserSystem.KratosAutoLinkByEmail,
		cfg.UserSystem.KratosAutoProvisionUser,
		time.Duration(cfg.UserSystem.KratosRequestTimeout)*time.Second,
		entClient,
	)
	if strings.EqualFold(strings.TrimSpace(cfg.UserSystem.AuthProvider), "kratos") {
		if strings.TrimSpace(cfg.UserSystem.KratosPublicURL) == "" {
			return fmt.Errorf("user_system.kratos_public_url is required when auth_provider=kratos")
		}
		if strings.TrimSpace(cfg.UserSystem.KratosAdminURL) == "" {
			return fmt.Errorf("user_system.kratos_admin_url is required when auth_provider=kratos")
		}
	}

	app := fiber.New(fiber.Config{
		BodyLimit:   100 * 1024 * 1024,
		JSONEncoder: sonic.Marshal,
		JSONDecoder: sonic.Unmarshal,
		ProxyHeader: cfg.Backend.ProxyHeader,
		TrustProxy:  cfg.Backend.EnableTrustProxy,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: cfg.Backend.TrustProxies,
		},
	})

	app.Use(compress.New(compress.Config{Level: compress.LevelBestSpeed}))
	app.Use(func(c fiber.Ctx) error {
		nonceBytes := make([]byte, 16)
		if _, err := rand.Read(nonceBytes); err != nil {
			return err
		}
		nonce := base64.StdEncoding.EncodeToString(nonceBytes)

		var cspConnectSrc strings.Builder
		cspConnectSrc.WriteString("'self'")
		for _, src := range cfg.Backend.CSPConnectSrc {
			cspConnectSrc.WriteString(" " + src)
		}

		c.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' https://challenges.cloudflare.com 'nonce-"+nonce+"'; "+
				"frame-src https://challenges.cloudflare.com; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https:; "+
				"connect-src "+cspConnectSrc.String()+"; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self';",
		)
		c.Locals("cspNonce", nonce)
		return c.Next()
	})

	allowedOrigins := make(map[string]struct{}, len(cfg.Backend.AllowCORS))
	for _, origin := range cfg.Backend.AllowCORS {
		allowedOrigins[origin] = struct{}{}
	}
	app.Use(cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			_, ok := allowedOrigins[origin]
			return ok
		},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-ID"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowCredentials: true,
	}))

	if cfg.Backend.AccessLog != "" {
		loggerConfig := logger.Config{
			Format:     cfg.Backend.AccessLog,
			TimeFormat: "2006-01-02 15:04:05",
			TimeZone:   "Local",
			CustomTags: map[string]logger.LogFunc{
				"bytesSent": func(output logger.Buffer, c fiber.Ctx, data *logger.Data, extra string) (int, error) {
					return output.WriteString(fmt.Sprintf("%d", len(c.Response().Body())))
				},
			},
		}

		if cfg.Backend.AccessLogPath != "" {
			accessLogFile, err := os.OpenFile(cfg.Backend.AccessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("open access log file: %w", err)
			}
			defer func() {
				_ = accessLogFile.Close()
			}()
			loggerConfig.Stream = accessLogFile
		}
		app.Use(logger.New(loggerConfig))
	}

	dbMgr := harukiDatabaseManager.NewHarukiToolboxDBManager(entClient, redisClient, mongoManager)
	apiHelper := harukiAPIHelper.NewHarukiToolboxRouterHelpers(
		app,
		dbMgr,
		smtpClient,
		sessionHandler,
		sekaiAPIClient,
		cfg.Others.PublicAPIAllowedKeys,
		cfg.MongoDB.PrivateApiSecret,
		cfg.MongoDB.PrivateApiUserAgent,
		cfg.HarukiProxy.UserAgent,
		cfg.HarukiProxy.Version,
		cfg.HarukiProxy.Secret,
		cfg.HarukiProxy.UnpackKey,
		cfg.Webhook.JWTSecret,
	)
	harukiAPI.RegisterRoutes(apiHelper)
	loadedRegions, failedRegions := harukiHandler.GetSuiteRestorerLoadStatus()
	if len(failedRegions) > 0 {
		mainLogger.Warnf("Suite restorer initialized with %d loaded region(s), %d failed region(s): %v", loadedRegions, len(failedRegions), failedRegions)
	} else {
		mainLogger.Infof("Suite restorer initialized with %d loaded region(s)", loadedRegions)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Backend.Host, cfg.Backend.Port)
	listenConfig := fiber.ListenConfig{DisableStartupMessage: true}
	if cfg.Backend.SSL {
		mainLogger.Infof("SSL enabled, starting HTTPS server at %s", addr)
		listenConfig.CertFile = cfg.Backend.SSLCert
		listenConfig.CertKeyFile = cfg.Backend.SSLKey
	} else {
		mainLogger.Infof("Starting HTTP server at %s", addr)
	}

	serverType := "HTTP"
	if cfg.Backend.SSL {
		serverType = "HTTPS"
	}
	listenErrCh := make(chan error, 1)
	go func() {
		listenErrCh <- app.Listen(addr, listenConfig)
	}()

	shutdownSignalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-listenErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("start %s server: %w", serverType, err)
		}
		return nil
	case <-shutdownSignalCtx.Done():
		mainLogger.Infof("shutdown signal received, stopping server")
	}

	shutdownTimeout := time.Duration(cfg.Backend.ShutdownTimeout) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	if err := <-listenErrCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server stopped with error: %w", err)
	}
	mainLogger.Infof("server shutdown completed")
	return nil
}
