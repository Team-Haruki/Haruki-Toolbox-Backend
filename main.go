package main

import (
	harukiBootstrap "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/bootstrap"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"os"
)

func main() {
	configPath, err := harukiConfig.LoadGlobalFromEnvOrDefault()
	if err != nil {
		bootstrapLogger := harukiLogger.NewLogger("Bootstrap", "INFO", os.Stdout)
		bootstrapLogger.Errorf("failed to load config from %s: %v", configPath, err)
		os.Exit(1)
	}

	mainLogger := harukiLogger.NewLogger("Main", harukiConfig.Cfg.Backend.LogLevel, os.Stdout)
	if err := harukiBootstrap.Run(harukiConfig.Cfg); err != nil {
		mainLogger.Errorf("server startup failed: %v", err)
		os.Exit(1)
	}
}
