package oauth2

import (
	"fmt"
	"haruki-suite/config"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	ProviderBuiltin = "builtin"
	ProviderHydra   = "hydra"
)

func OAuth2Provider() string {
	provider := strings.ToLower(strings.TrimSpace(config.Cfg.OAuth2.Provider))
	if provider == "" {
		return ProviderHydra
	}
	return provider
}

func UseHydraProvider() bool {
	return OAuth2Provider() == ProviderHydra
}

func HydraPublicEndpoint(endpointPath string) (string, error) {
	return buildHydraEndpoint(config.Cfg.OAuth2.HydraPublicURL, endpointPath)
}

func HydraAdminEndpoint(endpointPath string) (string, error) {
	return buildHydraEndpoint(config.Cfg.OAuth2.HydraAdminURL, endpointPath)
}

func HydraClientCredentials() (string, string) {
	return strings.TrimSpace(config.Cfg.OAuth2.HydraClientID), config.Cfg.OAuth2.HydraClientSecret
}

func HydraRequestTimeout() time.Duration {
	timeoutSeconds := config.Cfg.OAuth2.HydraRequestTimeoutSecond
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	return time.Duration(timeoutSeconds) * time.Second
}

func buildHydraEndpoint(baseURL, endpointPath string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("hydra base URL is not configured")
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid hydra base URL: %w", err)
	}
	if parsedBase.Scheme == "" || parsedBase.Host == "" {
		return "", fmt.Errorf("invalid hydra base URL")
	}

	cleanPath := endpointPath
	if cleanPath == "" {
		cleanPath = "/"
	}
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}

	basePath := strings.TrimRight(parsedBase.EscapedPath(), "/")
	if basePath == "" {
		parsedBase.Path = cleanPath
		return parsedBase.String(), nil
	}

	joined := path.Clean(strings.TrimRight(basePath, "/") + cleanPath)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	parsedBase.Path = joined
	return parsedBase.String(), nil
}
