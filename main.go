package main

import (
	"context"
	"fmt"
	privateApi "haruki-suite/api/private"
	publicApi "haruki-suite/api/public"
	webhookApi "haruki-suite/api/webhook"
	harukiConfig "haruki-suite/config"
	harukiLogger "haruki-suite/utils/logger"
	harukiMongo "haruki-suite/utils/mongo"
	harukiRedis "haruki-suite/utils/redis"
	harukiVersion "haruki-suite/version"
	"io"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

func main() {
	var logFile *os.File
	var loggerWriter io.Writer = os.Stdout
	if harukiConfig.Cfg.Backend.MainLogFile != "" {
		var err error
		logFile, err = os.OpenFile(harukiConfig.Cfg.Backend.MainLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			mainLogger := harukiLogger.NewLogger("Main", harukiConfig.Cfg.Backend.LogLevel, os.Stdout)
			mainLogger.Error("failed to open main log file: %v", err)
			os.Exit(1)
		}
		loggerWriter = io.MultiWriter(os.Stdout, logFile)
		defer logFile.Close()
	}
	mainLogger := harukiLogger.NewLogger("Main", harukiConfig.Cfg.Backend.LogLevel, loggerWriter)
	mainLogger.Info(fmt.Sprintf("========================= Haruki Suite Backend %s =========================", harukiVersion.Version))
	mainLogger.Info("Powered By Haruki Dev Team")
	mainLogger.Info("Haruki Suite Backend Main Access Log Level: ", harukiConfig.Cfg.Backend.LogLevel)
	mainLogger.Info("Haruki Suite Backend Main Access Log Save Path: ", harukiConfig.Cfg.Backend.MainLogFile)
	mainLogger.Info("Go Fiber Access Log Save Path: ", harukiConfig.Cfg.Backend.AccessLogPath)
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
		mainLogger.Error("Failed to init MongoDB: %v", err)
		os.Exit(1)
	}

	redisClient := harukiRedis.NewRedisClient(harukiConfig.Cfg.Redis)
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		mainLogger.Error("Failed to connect Redis: %v", err)
		os.Exit(1)
	}

	app := fiber.New()
	var accessLogFile *os.File
	if harukiConfig.Cfg.Backend.AccessLog != "" {
		loggerConfig := logger.Config{Format: harukiConfig.Cfg.Backend.AccessLog}
		if harukiConfig.Cfg.Backend.AccessLogPath != "" {
			var err error
			accessLogFile, err = os.OpenFile(harukiConfig.Cfg.Backend.AccessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				mainLogger.Error("Failed to open access log file: %v", err)
				os.Exit(1)
			}
			loggerConfig.Output = accessLogFile
		}
		app.Use(logger.New(loggerConfig))
	}
	if accessLogFile != nil {
		defer accessLogFile.Close()
	}
	privateApi.RegisterRoutes(app, mongoManager, harukiConfig.Cfg.MongoDB.PrivateApiSecret, harukiConfig.Cfg.MongoDB.PrivateApiUserAgent)
	webhookApi.RegisterRoutes(app, mongoManager, harukiConfig.Cfg.Webhook.JWTSecret)
	publicAPI := &publicApi.HarukiPublicAPI{
		Mongo:       mongoManager,
		Redis:       redisClient,
		AllowedKeys: harukiConfig.Cfg.Others.PublicAPIAllowedKeys,
	}
	publicAPI.RegisterRoutes(app)

	addr := fmt.Sprintf("%s:%d", harukiConfig.Cfg.Backend.Host, harukiConfig.Cfg.Backend.Port)
	if err := app.Listen(addr); err != nil {
		mainLogger.Error("Failed to start server: %v", err)
		os.Exit(1)
	}
}
