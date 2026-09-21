package oauth2

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	ProviderHydra              = "hydra"
	defaultHydraRequestTimeout = 10 * time.Second
)

// HydraConfigOptions is copied by NewHydraConfig. Callers may freely reuse or
// mutate the input after construction without changing the resulting config.
type HydraConfigOptions struct {
	Provider       string
	PublicURL      string
	BrowserURL     string
	AdminURL       string
	ClientID       string
	ClientSecret   string
	RequestTimeout time.Duration
}

// HydraConfig is an immutable, process-scoped view of Hydra endpoints and
// credentials. It is constructed by the composition root and explicitly passed
// to consumers instead of reading mutable global application configuration.
type HydraConfig struct {
	provider       string
	publicURL      string
	browserURL     string
	adminURL       string
	clientID       string
	clientSecret   string
	requestTimeout time.Duration
	httpClient     *http.Client
}

func NewHydraConfig(options HydraConfigOptions) *HydraConfig {
	provider := strings.ToLower(strings.TrimSpace(options.Provider))
	if provider == "" {
		provider = ProviderHydra
	}
	publicURL := strings.TrimSpace(options.PublicURL)
	browserURL := strings.TrimSpace(options.BrowserURL)
	if browserURL == "" {
		browserURL = publicURL
	}
	timeout := options.RequestTimeout
	if timeout <= 0 {
		timeout = defaultHydraRequestTimeout
	}
	return &HydraConfig{
		provider:       provider,
		publicURL:      publicURL,
		browserURL:     browserURL,
		adminURL:       strings.TrimSpace(options.AdminURL),
		clientID:       strings.TrimSpace(options.ClientID),
		clientSecret:   options.ClientSecret,
		requestTimeout: timeout,
		httpClient:     &http.Client{Timeout: timeout},
	}
}

func (c *HydraConfig) Provider() string {
	if c == nil || c.provider == "" {
		return ProviderHydra
	}
	return c.provider
}

func (c *HydraConfig) Enabled() bool {
	return c.Provider() == ProviderHydra
}

func (c *HydraConfig) PublicEndpoint(endpointPath string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("hydra config is not initialized")
	}
	return buildHydraEndpoint(c.publicURL, endpointPath)
}

func (c *HydraConfig) BrowserEndpoint(endpointPath string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("hydra config is not initialized")
	}
	return buildHydraEndpoint(c.browserURL, endpointPath)
}

func (c *HydraConfig) AdminEndpoint(endpointPath string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("hydra config is not initialized")
	}
	return buildHydraEndpoint(c.adminURL, endpointPath)
}

func (c *HydraConfig) ClientCredentials() (string, string) {
	if c == nil {
		return "", ""
	}
	return c.clientID, c.clientSecret
}

func (c *HydraConfig) RequestTimeout() time.Duration {
	if c == nil || c.requestTimeout <= 0 {
		return defaultHydraRequestTimeout
	}
	return c.requestTimeout
}

// Do uses the single timeout-configured client owned by this immutable config.
// http.Client is safe for concurrent use and should be reused across requests.
func (c *HydraConfig) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.httpClient == nil {
		return nil, fmt.Errorf("hydra config is not initialized")
	}
	return c.httpClient.Do(req)
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
