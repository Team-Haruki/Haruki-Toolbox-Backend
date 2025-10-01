package main

import (
	"context"
	"fmt"
	harukiApi "haruki-suite/api"
	harukiConfig "haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiDatabaseManager "haruki-suite/utils/database"
	harukiMongo "haruki-suite/utils/database/mongo"
	dbManager "haruki-suite/utils/database/postgresql"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	harukiSekaiAPIClient "haruki-suite/utils/sekaiapi"
	harukiSMTP "haruki-suite/utils/smtp"
	harukiVersion "haruki-suite/version"

	"io"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	_ "github.com/lib/pq"
)

func main() {
	var logFile *os.File
	var loggerWriter io.Writer = os.Stdout
	if harukiConfig.Cfg.Backend.MainLogFile != "" {
		var err error
		logFile, err = os.OpenFile(harukiConfig.Cfg.Backend.MainLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			mainLogger := harukiLogger.NewLogger("Main", harukiConfig.Cfg.Backend.LogLevel, os.Stdout)
			mainLogger.Errorf("failed to open main log file: %v", err)
			os.Exit(1)
		}
		loggerWriter = io.MultiWriter(os.Stdout, logFile)
		defer logFile.Close()
	}
	mainLogger := harukiLogger.NewLogger("Main", harukiConfig.Cfg.Backend.LogLevel, loggerWriter)
	mainLogger.Infof(fmt.Sprintf("========================= Haruki Toolbox Backend %s =========================", harukiVersion.Version))
	mainLogger.Infof("Powered By Haruki Dev Team")

	sekaiAPIClient := harukiSekaiAPIClient.NewHarukiSekaiAPIClient(
		harukiConfig.Cfg.SekaiAPI.APIEndpoint,
		harukiConfig.Cfg.SekaiAPI.APIToken,
	)
	mongoManager, err := harukiMongo.NewMongoDBManager(
		context.Background(),
		harukiConfig.Cfg.MongoDB.URL,
		harukiConfig.Cfg.MongoDB.DB,
		harukiConfig.Cfg.MongoDB.Suite,
		harukiConfig.Cfg.MongoDB.Mysekai,
		harukiConfig.Cfg.MongoDB.Webhook,
		harukiConfig.Cfg.MongoDB.WebhookUser,
	)
	if err != nil {
		mainLogger.Errorf("Failed to init MongoDB: %v", err)
		os.Exit(1)
	}
	redisClient := harukiRedis.NewRedisClient(harukiConfig.Cfg.Redis)
	entClient, err := dbManager.Open(harukiConfig.Cfg.UserSystem.DBType, harukiConfig.Cfg.UserSystem.DBURL)
	if err != nil {
		mainLogger.Errorf("Failed to init PostgreSQL: %v", err)
		os.Exit(1)
	}
	if err := entClient.Schema.Create(context.Background()); err != nil {
		mainLogger.Errorf("Failed creating schema resources: %v", err)
		os.Exit(1)
	}
	defer entClient.Close()
	smtpClient := harukiSMTP.NewSMTPClient(harukiConfig.Cfg.UserSystem.SMTP)
	sessionHandler := harukiAPIHelper.NewSessionHandler(redisClient.Redis, harukiConfig.Cfg.UserSystem.SessionSignToken)

	app := fiber.New(fiber.Config{
		BodyLimit: 30 * 1024 * 1024,
	})
	allowedOrigins := make(map[string]struct{})
	for _, origin := range harukiConfig.Cfg.Backend.AllowCORS {
		allowedOrigins[origin] = struct{}{}
	}
	app.Use(cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool {
			_, ok := allowedOrigins[origin]
			return ok
		},
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
		AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS",
		AllowCredentials: true,
	}))

	if harukiConfig.Cfg.Backend.AccessLog != "" {
		loggerConfig := logger.Config{Format: harukiConfig.Cfg.Backend.AccessLog}
		if harukiConfig.Cfg.Backend.AccessLogPath != "" {
			accessLogFile, err := os.OpenFile(harukiConfig.Cfg.Backend.AccessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				mainLogger.Errorf("Failed to open access log file: %v", err)
				os.Exit(1)
			}
			defer accessLogFile.Close()
			loggerConfig.Output = accessLogFile
		}
		app.Use(logger.New(loggerConfig))
	}

	dbMgr := harukiDatabaseManager.NewHarukiToolboxDBManager(entClient, redisClient, mongoManager)

	apiHelper := harukiAPIHelper.NewHarukiToolboxDBHelpers(
		app,
		dbMgr,
		smtpClient,
		sessionHandler,
		sekaiAPIClient,
		harukiConfig.Cfg.Others.PublicAPIAllowedKeys,
		harukiConfig.Cfg.MongoDB.PrivateApiSecret,
		harukiConfig.Cfg.MongoDB.PrivateApiUserAgent,
		harukiConfig.Cfg.HarukiProxy.UserAgent,
		harukiConfig.Cfg.HarukiProxy.Version,
		harukiConfig.Cfg.HarukiProxy.Secret,
		harukiConfig.Cfg.Webhook.JWTSecret,
	)
	harukiApi.RegisterRoutes(apiHelper)

	addr := fmt.Sprintf("%s:%d", harukiConfig.Cfg.Backend.Host, harukiConfig.Cfg.Backend.Port)
	if harukiConfig.Cfg.Backend.SSL {
		if err := app.ListenTLS(addr, harukiConfig.Cfg.Backend.SSLCert, harukiConfig.Cfg.Backend.SSLKey); err != nil {
			mainLogger.Errorf("Failed to start HTTPS server: %v", err)
			os.Exit(1)
		}
	} else {
		if err := app.Listen(addr); err != nil {
			mainLogger.Errorf("Failed to start HTTP server: %v", err)
			os.Exit(1)
		}
	}
}
