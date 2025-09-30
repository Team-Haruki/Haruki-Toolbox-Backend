package main

import (
	"context"
	"fmt"
	publicApi "haruki-suite/api/public"
	uploadApi "haruki-suite/api/upload"
	privateApi "haruki-suite/api/user"
	webhookApi "haruki-suite/api/webhook"
	harukiConfig "haruki-suite/config"
	harukiMongo "haruki-suite/utils/database/mongo"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	harukiVersion "haruki-suite/version"
	"io"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
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
			mainLogger.Errorf("failed to open main log file: %v", err)
			os.Exit(1)
		}
		loggerWriter = io.MultiWriter(os.Stdout, logFile)
		defer logFile.Close()
	}
	mainLogger := harukiLogger.NewLogger("Main", harukiConfig.Cfg.Backend.LogLevel, loggerWriter)
	mainLogger.Infof(fmt.Sprintf("========================= Haruki Toolbox Backend %s =========================", harukiVersion.Version))
	mainLogger.Infof("Powered By Haruki Dev Team")
	mainLogger.Infof("Haruki Suite Backend Main Access Log Level: %s", harukiConfig.Cfg.Backend.LogLevel)
	mainLogger.Infof("Haruki Suite Backend Main Access Log Save Path: %s", harukiConfig.Cfg.Backend.MainLogFile)
	mainLogger.Infof("Go Fiber Access Log Save Path: %s", harukiConfig.Cfg.Backend.AccessLogPath)
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
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		mainLogger.Errorf("Failed to connect Redis: %v", err)
		os.Exit(1)
	}

	app := fiber.New(fiber.Config{
		BodyLimit: 20 * 1024 * 1024,
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

	var accessLogFile *os.File
	if harukiConfig.Cfg.Backend.AccessLog != "" {
		loggerConfig := logger.Config{Format: harukiConfig.Cfg.Backend.AccessLog}
		if harukiConfig.Cfg.Backend.AccessLogPath != "" {
			var err error
			accessLogFile, err = os.OpenFile(harukiConfig.Cfg.Backend.AccessLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				mainLogger.Errorf("Failed to open access log file: %v", err)
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
	uploadApi.RegisterRoutes(app, mongoManager, redisClient, &harukiConfig.Cfg.HarukiProxy.UserAgent, &harukiConfig.Cfg.HarukiProxy.Version, &harukiConfig.Cfg.HarukiProxy.Secret)
	publicAPI := &publicApi.HarukiPublicAPI{
		Mongo:       mongoManager,
		Redis:       redisClient,
		AllowedKeys: harukiConfig.Cfg.Others.PublicAPIAllowedKeys,
	}
	publicAPI.RegisterRoutes(app)

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
