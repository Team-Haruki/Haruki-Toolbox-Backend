package bootstrap

import (
	"context"
	"errors"
	"fmt"
	harukiAPI "github.com/Team-Haruki/Haruki-Toolbox-Backend/api"
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiDatabaseManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiMongo "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/mongo"
	neopgManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiSekaiAPIClient "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekaiapi"
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
	harukiVersion "github.com/Team-Haruki/Haruki-Toolbox-Backend/version"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	_ "github.com/lib/pq"
)

const resourceCloseTimeout = 5 * time.Second

func Run(cfg harukiConfig.Config) error {
	if err := validateOAuth2ProviderConfig(cfg); err != nil {
		return err
	}
	if err := validateUserSystemConfig(cfg); err != nil {
		return err
	}

	loggerWriter, closeMainLogFile, err := openMainLogWriter(cfg.Backend.MainLogFile)
	if err != nil {
		return fmt.Errorf("open main log file: %w", err)
	}
	defer func() {
		_ = closeMainLogFile()
	}()

	// Set global log level and file writer for NewLoggerFromGlobal
	harukiLogger.SetGlobalLogLevel(cfg.Backend.LogLevel)
	harukiLogger.SetGlobalFileWriter(loggerWriter)

	mainLogger := harukiLogger.NewLogger("Main", cfg.Backend.LogLevel, loggerWriter)
	mainLogger.Infof("%s", fmt.Sprintf("========================= Haruki Toolbox Backend %s =========================", harukiVersion.Version))
	mainLogger.Infof("Powered By Haruki Dev Team")

	sekaiAPIClient := harukiSekaiAPIClient.NewHarukiSekaiAPIClient(cfg.SekaiAPI.APIEndpoint, cfg.SekaiAPI.APIToken)
	mongoCtx, cancelMongoInit := startupContext()
	mongoManager, err := harukiMongo.NewMongoDBManager(
		mongoCtx,
		cfg.MongoDB.URL,
		cfg.MongoDB.DB,
		cfg.MongoDB.Suite,
		cfg.MongoDB.Mysekai,
	)
	cancelMongoInit()
	if err != nil {
		return fmt.Errorf("init MongoDB: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), resourceCloseTimeout)
		defer cancel()
		_ = mongoManager.Disconnect(closeCtx)
	}()

	redisClient := harukiRedis.NewRedisClient(cfg.Redis)
	defer func() {
		_ = redisClient.Close()
	}()
	redisCtx, cancelRedisInit := startupContext()
	if err := ensureRedisReady(redisCtx, redisClient); err != nil {
		cancelRedisInit()
		return fmt.Errorf("init Redis: %w", err)
	}
	cancelRedisInit()
	entClient, err := dbManager.Open(cfg.UserSystem.DBType, cfg.UserSystem.DBURL)
	if err != nil {
		return fmt.Errorf("init PostgreSQL: %w", err)
	}
	defer func() {
		_ = entClient.Close()
	}()
	if cfg.Backend.AutoMigrate {
		schemaCtx, cancelSchema := startupContext()
		if err := entClient.Schema.Create(schemaCtx); err != nil {
			cancelSchema()
			return fmt.Errorf("create schema resources: %w", err)
		}
		cancelSchema()
		mainLogger.Infof("auto schema migration completed")
	} else {
		mainLogger.Infof("auto schema migration disabled")
		existsCtx, cancelExists := startupContext()
		exists, existsErr := usersTableExists(existsCtx, entClient)
		cancelExists()
		if existsErr != nil {
			return fmt.Errorf("check schema state when auto_migrate disabled: %w", existsErr)
		}
		if !exists {
			return fmt.Errorf("database schema is not initialized (users table missing) while backend.auto_migrate=false")
		}
	}
	usersSchemaCtx, cancelUsersSchema := startupContext()
	if err := ensureUsersSchemaCompatibility(usersSchemaCtx, entClient, cfg.Backend.AutoMigrate); err != nil {
		cancelUsersSchema()
		return fmt.Errorf("ensure users schema compatibility: %w", err)
	}
	cancelUsersSchema()
	webhookSchemaCtx, cancelWebhookSchema := startupContext()
	if err := ensureWebhookSchemaCompatibility(webhookSchemaCtx, entClient, cfg.Backend.AutoMigrate); err != nil {
		cancelWebhookSchema()
		return fmt.Errorf("ensure webhook schema compatibility: %w", err)
	}
	cancelWebhookSchema()
	grantsCleanupCtx, cancelGrantsCleanup := startupContext()
	if deleted, err := entClient.CleanupExpiredGameAccountDataGrants(grantsCleanupCtx, time.Now().UTC()); err != nil {
		mainLogger.Warnf("failed to cleanup expired game account data grants: %v", err)
	} else if deleted > 0 {
		mainLogger.Infof("cleaned up %d expired game account data grant(s)", deleted)
	}
	cancelGrantsCleanup()

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
	sessionHandler.ConfigureAuthProxy(
		cfg.UserSystem.AuthProxyEnabled,
		cfg.UserSystem.AuthProxyTrustedHeader,
		cfg.UserSystem.AuthProxyTrustedValue,
		cfg.UserSystem.AuthProxySubjectHeader,
		cfg.UserSystem.AuthProxyNameHeader,
		cfg.UserSystem.AuthProxyEmailHeader,
		cfg.UserSystem.AuthProxyEmailVerifiedHeader,
		cfg.UserSystem.AuthProxyUserIDHeader,
	)
	sessionHandler.ConfigureAuthProxySessionHeader(cfg.UserSystem.AuthProxySessionHeader)
	app, closeAccessLogFile, err := newFiberApp(cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = closeAccessLogFile()
	}()

	dbMgr := harukiDatabaseManager.NewHarukiToolboxDBManager(entClient, redisClient, mongoManager)
	if botDBURL := strings.TrimSpace(cfg.HarukiBot.DBURL); botDBURL != "" {
		botClient, botErr := neopgManager.Open(cfg.UserSystem.DBType, botDBURL)
		if botErr != nil {
			return fmt.Errorf("init Bot PostgreSQL: %w", botErr)
		}
		defer func() {
			_ = botClient.Close()
		}()
		if cfg.Backend.AutoMigrate {
			botSchemaCtx, cancelBotSchema := startupContext()
			if err := botClient.Schema.Create(botSchemaCtx); err != nil {
				cancelBotSchema()
				return fmt.Errorf("create bot schema resources: %w", err)
			}
			cancelBotSchema()
			mainLogger.Infof("bot schema migration completed")
		}
		dbMgr.BotDB = botClient
	}
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
		cfg.Webhook.Enabled,
	)
	apiHelper.BotRegistrationEnabled = cfg.HarukiBot.EnableRegistration
	apiHelper.BotCredentialSignToken = cfg.HarukiBot.CredentialSignToken
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
