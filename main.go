package main

import (
	"context"
	// Embed the IANA timezone database so time.LoadLocation works regardless of
	// whether the host/container ships system zoneinfo (used by admin statistics
	// timeseries bucketing).
	_ "time/tzdata"

	harukiBootstrap "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/bootstrap"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath, err := harukiConfig.LoadGlobalFromEnvOrDefault()
	if err != nil {
		bootstrapLogger := harukiLogger.NewLogger("Bootstrap", "INFO", os.Stdout)
		bootstrapLogger.Errorf("failed to load config from %s: %v", configPath, err)
		return 1
	}

	mainLogger := harukiLogger.NewLogger("Main", harukiConfig.Cfg.Backend.LogLevel, os.Stdout)
	application, err := harukiBootstrap.Build(harukiConfig.Cfg)
	if err != nil {
		mainLogger.Errorf("server startup failed: %v", err)
		return 1
	}

	shutdownSignalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	serveErr := application.Serve(shutdownSignalCtx)
	stopSignals()
	closeErr := application.Close()
	if serveErr != nil {
		mainLogger.Errorf("server startup failed: %v", serveErr)
		if closeErr != nil {
			mainLogger.Errorf("server shutdown failed: %v", closeErr)
		}
		return 1
	}
	if closeErr != nil {
		mainLogger.Errorf("server shutdown failed: %v", closeErr)
		return 1
	}
	return 0
}
