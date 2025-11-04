package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
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
		defer func(logFile *os.File) {
			_ = logFile.Close()
		}(logFile)
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
	defer func(entClient *dbManager.Client) {
		_ = entClient.Close()
	}(entClient)
	smtpClient := harukiSMTP.NewSMTPClient(harukiConfig.Cfg.UserSystem.SMTP)
	sessionHandler := harukiAPIHelper.NewSessionHandler(redisClient.Redis, harukiConfig.Cfg.UserSystem.SessionSignToken)

	app := fiber.New(fiber.Config{BodyLimit: 30 * 1024 * 1024})
	app.Use(func(c fiber.Ctx) error {
		nonceBytes := make([]byte, 16)
		if _, err := rand.Read(nonceBytes); err != nil {
			return err
		}
		nonce := base64.StdEncoding.EncodeToString(nonceBytes)
		c.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' https://challenges.cloudflare.com 'nonce-"+nonce+"'; "+
				"frame-src https://challenges.cloudflare.com; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https:; "+
				"connect-src 'self' https://your-api-domain.com; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self';",
		)
		c.Locals("cspNonce", nonce)
		return c.Next()
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
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
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
			defer func(accessLogFile *os.File) {
				_ = accessLogFile.Close()
			}(accessLogFile)
			loggerConfig.Stream = accessLogFile
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
		harukiConfig.Cfg.HarukiProxy.UnpackKey,
		harukiConfig.Cfg.Webhook.JWTSecret,
	)
	harukiApi.RegisterRoutes(apiHelper)

	addr := fmt.Sprintf("%s:%d", harukiConfig.Cfg.Backend.Host, harukiConfig.Cfg.Backend.Port)

	listenConfig := fiber.ListenConfig{
		DisableStartupMessage: true,
	}
	if harukiConfig.Cfg.Backend.SSL {
		mainLogger.Infof("SSL enabled, starting HTTPS server at %s", addr)
		listenConfig.CertFile = harukiConfig.Cfg.Backend.SSLCert
		listenConfig.CertKeyFile = harukiConfig.Cfg.Backend.SSLKey
		if err := app.Listen(addr, listenConfig); err != nil {
			mainLogger.Errorf("failed to start HTTPS server: %v", err)
			os.Exit(1)
		}
	} else {
		mainLogger.Infof("Starting HTTP server at %s", addr)
		if err := app.Listen(addr, listenConfig); err != nil {
			mainLogger.Errorf("failed to start HTTP server: %v", err)
			os.Exit(1)
		}
	}
}
