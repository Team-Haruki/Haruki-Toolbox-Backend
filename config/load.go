package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func Load(configPath string) (Config, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		return Config{}, fmt.Errorf("config path is empty")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config file %q: %w", path, err)
	}
	expandedContent := expandEnvPreservingUnknown(string(content))
	cfg := defaultConfig()
	if err := yaml.Unmarshal([]byte(expandedContent), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config file %q: %w", path, err)
	}
	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}
	if err := normalizeConfigDefaults(&cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func LoadGlobal(configPath string) error {
	cfg, err := Load(configPath)
	if err != nil {
		return err
	}
	Cfg = cfg
	return nil
}

func LoadGlobalFromEnvOrDefault() (string, error) {
	configPath := resolveConfigPathFromEnvOrDefault()
	if err := LoadGlobal(configPath); err != nil {
		return configPath, err
	}
	return configPath, nil
}
