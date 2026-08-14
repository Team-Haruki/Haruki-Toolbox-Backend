package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiDatabaseManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiMongo "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/mongo"
	neopgManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	perfdebug "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/perfdebug"
	harukiSekaiAPIClient "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekaiapi"
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
	harukiVersion "github.com/Team-Haruki/Haruki-Toolbox-Backend/version"

	"github.com/gofiber/fiber/v3"
	_ "github.com/lib/pq"
)

const resourceCloseTimeout = 5 * time.Second

// applicationResources is a bootstrap-only record of concrete process
// resources. It deliberately stays private to the composition root and is not
// passed into business modules as a dependency container.
type applicationResources struct {
	logger          *harukiLogger.Logger
	sekaiAPIClient  *harukiSekaiAPIClient.HarukiSekaiAPIClient
	mongoManager    *harukiMongo.MongoDBManager
	mongoPoolStats  *harukiMongo.PoolStats
	redisClient     *harukiRedis.HarukiRedisManager
	toolboxSQLDB    *sql.DB
	toolboxClient   *dbManager.Client
	smtpClient      *harukiSMTP.HarukiSMTPClient
	fiberApp        *fiber.App
	databaseManager *harukiDatabaseManager.HarukiToolboxDBManager
	botSQLDB        *sql.DB
}

// acquireApplicationResources opens the long-lived resources needed before
// HTTP/module assembly. Each closer is registered immediately after acquisition
// so a later Build failure unwinds the exact subset that was opened.
func acquireApplicationResources(cfg harukiConfig.Config, owner *Application) (*applicationResources, error) {
	resources := &applicationResources{}

	loggerWriter, closeMainLogFile, err := openMainLogWriter(cfg.Backend.MainLogFile)
	if err != nil {
		return nil, fmt.Errorf("open main log file: %w", err)
	}
	owner.addResourceCloser("main log file", closeMainLogFile)

	// Set global log level and file writer for NewLoggerFromGlobal.
	harukiLogger.SetGlobalLogLevel(cfg.Backend.LogLevel)
	harukiLogger.SetGlobalFileWriter(loggerWriter)
	perfdebug.SetEnabled(cfg.Backend.ProfilingEnabled)

	resources.logger = harukiLogger.NewLogger("Main", cfg.Backend.LogLevel, loggerWriter)
	resources.logger.Infof("%s", fmt.Sprintf("========================= Haruki Toolbox Backend %s =========================", harukiVersion.Version))
	resources.logger.Infof("Build commit: %s, built at: %s", harukiVersion.Commit, harukiVersion.BuildDate)
	resources.logger.Infof("Powered By Haruki Dev Team")

	resources.sekaiAPIClient = harukiSekaiAPIClient.NewHarukiSekaiAPIClient(cfg.SekaiAPI.APIEndpoint, cfg.SekaiAPI.APIToken)

	var mongoOpts []harukiMongo.MongoOption
	if cfg.Backend.ProfilingEnabled {
		resources.mongoPoolStats = harukiMongo.NewPoolStats()
		mongoOpts = append(mongoOpts, harukiMongo.WithPoolMonitor(resources.mongoPoolStats.Monitor()))
	}
	mongoCtx, cancelMongoInit := startupContext()
	resources.mongoManager, err = harukiMongo.NewMongoDBManager(
		mongoCtx,
		cfg.MongoDB.URL,
		cfg.MongoDB.DB,
		cfg.MongoDB.Suite,
		cfg.MongoDB.Mysekai,
		mongoOpts...,
	)
	cancelMongoInit()
	if err != nil {
		return nil, fmt.Errorf("init MongoDB: %w", err)
	}
	owner.addResourceCloser("MongoDB", func() error {
		closeCtx, cancel := context.WithTimeout(context.Background(), resourceCloseTimeout)
		defer cancel()
		return resources.mongoManager.Disconnect(closeCtx)
	})

	resources.redisClient = harukiRedis.NewRedisClient(cfg.Redis, cfg.UserSystem.SessionSignToken)
	owner.addResourceCloser("Redis", resources.redisClient.Close)
	redisCtx, cancelRedisInit := startupContext()
	if err := ensureRedisReady(redisCtx, resources.redisClient); err != nil {
		cancelRedisInit()
		return nil, fmt.Errorf("init Redis: %w", err)
	}
	cancelRedisInit()

	resources.toolboxSQLDB, err = openTunedSQLDB(cfg.UserSystem.DBType, cfg.UserSystem.DBURL, 50, 10)
	if err != nil {
		return nil, fmt.Errorf("init PostgreSQL: %w", err)
	}
	resources.toolboxClient = dbManager.NewClient(dbManager.Driver(entsql.OpenDB(cfg.UserSystem.DBType, resources.toolboxSQLDB)))
	owner.addResourceCloser("Toolbox PostgreSQL", resources.toolboxClient.Close)
	if err := prepareToolboxDatabase(cfg, resources.toolboxClient, resources.logger); err != nil {
		return nil, err
	}

	resources.smtpClient = harukiSMTP.NewSMTPClient(cfg.UserSystem.SMTP)
	resources.databaseManager = harukiDatabaseManager.NewHarukiToolboxDBManager(
		resources.toolboxClient,
		resources.redisClient,
		resources.mongoManager,
	)
	return resources, nil
}

func prepareToolboxDatabase(cfg harukiConfig.Config, entClient *dbManager.Client, logger *harukiLogger.Logger) error {
	if cfg.Backend.AutoMigrate {
		schemaCtx, cancelSchema := startupContext()
		if err := entClient.Schema.Create(schemaCtx); err != nil {
			cancelSchema()
			return fmt.Errorf("create schema resources: %w", err)
		}
		cancelSchema()
		logger.Infof("auto schema migration completed")
	} else {
		logger.Infof("auto schema migration disabled")
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
		logger.Warnf("failed to cleanup expired game account data grants: %v", err)
	} else if deleted > 0 {
		logger.Infof("cleaned up %d expired game account data grant(s)", deleted)
	}
	cancelGrantsCleanup()
	return nil
}

// acquireHTTPResources constructs Fiber and owns the optional access-log file.
func (r *applicationResources) acquireHTTPResources(cfg harukiConfig.Config, owner *Application) error {
	app, closeAccessLogFile, err := newFiberApp(cfg)
	if err != nil {
		return err
	}
	r.fiberApp = app
	owner.addResourceCloser("access log file", closeAccessLogFile)
	return nil
}

// acquireBotDatabase opens the independent HarukiBot Ent database when configured.
func (r *applicationResources) acquireBotDatabase(cfg harukiConfig.Config, owner *Application) error {
	botDBURL := strings.TrimSpace(cfg.HarukiBot.DBURL)
	if botDBURL == "" {
		return nil
	}

	botSQLDB, err := openTunedSQLDB(cfg.UserSystem.DBType, botDBURL, 20, 5)
	if err != nil {
		return fmt.Errorf("init Bot PostgreSQL: %w", err)
	}
	r.botSQLDB = botSQLDB
	botClient := neopgManager.NewClient(neopgManager.Driver(entsql.OpenDB(cfg.UserSystem.DBType, botSQLDB)))
	owner.addResourceCloser("Bot PostgreSQL", botClient.Close)
	if cfg.Backend.AutoMigrate {
		botSchemaCtx, cancelBotSchema := startupContext()
		if err := botClient.Schema.Create(botSchemaCtx); err != nil {
			cancelBotSchema()
			return fmt.Errorf("create bot schema resources: %w", err)
		}
		cancelBotSchema()
		r.logger.Infof("bot schema migration completed")
	}
	r.databaseManager.BotDB = botClient
	return nil
}
