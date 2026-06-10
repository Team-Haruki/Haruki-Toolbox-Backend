package config

import (
	"os"
	"path/filepath"
	"strings"
)

const defaultConfigFilename = "haruki-toolbox-configs.yaml"

func findConfigPath(filename string) string {
	wd, err := os.Getwd()
	if err != nil {
		return filename
	}
	dir := wd
	for {
		candidate := filepath.Join(dir, filename)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filename
}

func resolveConfigPathFromEnvOrDefault() string {
	configPath := strings.TrimSpace(os.Getenv("HARUKI_CONFIG_PATH"))
	if configPath == "" {
		configPath = findConfigPath(defaultConfigFilename)
	}
	return configPath
}
