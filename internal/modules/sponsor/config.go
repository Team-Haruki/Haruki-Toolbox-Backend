package sponsor

import (
	"strings"
	"time"
)

const (
	defaultAfdianAPIBaseURL     = "https://afdian.com/api/open"
	defaultAfdianRequestTimeout = 10 * time.Second
	defaultAfdianSyncInterval   = 5 * time.Minute
)

// AfdianConfigOptions contains the process-start settings consumed by Afdian
// HTTP handlers and the background sponsor synchronizer.
type AfdianConfigOptions struct {
	UserID         string
	APIToken       string
	APIBaseURL     string
	RequestTimeout time.Duration
	WebhookSecret  string
	SyncEnabled    bool
	SyncInterval   time.Duration
}

// AfdianConfig is an immutable, module-owned copy of the Afdian startup
// settings. Private fields prevent handlers from observing later mutations of
// process-wide configuration.
type AfdianConfig struct {
	userID         string
	apiToken       string
	apiBaseURL     string
	requestTimeout time.Duration
	webhookSecret  string
	syncEnabled    bool
	syncInterval   time.Duration
}

func NewAfdianConfig(options AfdianConfigOptions) AfdianConfig {
	return AfdianConfig{
		userID:         options.UserID,
		apiToken:       options.APIToken,
		apiBaseURL:     options.APIBaseURL,
		requestTimeout: options.RequestTimeout,
		webhookSecret:  options.WebhookSecret,
		syncEnabled:    options.SyncEnabled,
		syncInterval:   options.SyncInterval,
	}
}

func (c AfdianConfig) credentialsConfigured() bool {
	return strings.TrimSpace(c.userID) != "" && strings.TrimSpace(c.apiToken) != ""
}

func (c AfdianConfig) baseURL() string {
	baseURL := strings.TrimRight(strings.TrimSpace(c.apiBaseURL), "/")
	if baseURL == "" {
		return defaultAfdianAPIBaseURL
	}
	return baseURL
}

func (c AfdianConfig) timeout() time.Duration {
	// Preserve the historical ten-second minimum used by Afdian requests.
	if c.requestTimeout < defaultAfdianRequestTimeout {
		return defaultAfdianRequestTimeout
	}
	return c.requestTimeout
}

func (c AfdianConfig) WebhookSecret() string {
	return strings.TrimSpace(c.webhookSecret)
}

func (c AfdianConfig) CredentialsConfigured() bool {
	return c.credentialsConfigured()
}

func (c AfdianConfig) SyncEnabled() bool {
	return c.syncEnabled
}

func (c AfdianConfig) SyncInterval() time.Duration {
	if c.syncInterval <= 0 {
		return defaultAfdianSyncInterval
	}
	return c.syncInterval
}
