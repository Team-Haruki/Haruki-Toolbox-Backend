package config

import (
	"fmt"
	"strings"
)

func defaultConfig() Config {
	return Config{
		Backend: BackendConfig{
			AutoMigrate:     false,
			ShutdownTimeout: 10,
		},
		OAuth2: OAuth2Config{
			Provider:                  "hydra",
			HydraRequestTimeoutSecond: 10,
		},
		UserSystem: UserSystemConfig{
			AuthProvider:                 "kratos",
			AuthProxyTrustedHeader:       "X-Auth-Proxy-Secret",
			AuthProxySubjectHeader:       "X-Kratos-Identity-Id",
			AuthProxyNameHeader:          "X-User-Name",
			AuthProxyEmailHeader:         "X-User-Email",
			AuthProxyEmailVerifiedHeader: "X-User-Email-Verified",
			AuthProxyUserIDHeader:        "X-User-Id",
			KratosRequestTimeout:         10,
			KratosSessionHeader:          "X-Session-Token",
			KratosSessionCookie:          "ory_kratos_session",
			KratosAutoLinkByEmail:        true,
			KratosAutoProvisionUser:      true,
			SMTP: SMTPConfig{
				TimeoutSeconds: 10,
			},
		},
		Webhook: WebhookConfig{
			Enabled: true,
		},
		Afdian: AfdianConfig{
			APIBaseURL:           "https://afdian.com/api/open",
			RequestTimeoutSecond: 10,
			SyncEnabled:          true,
			SyncIntervalSeconds:  300,
		},
		Subscription: SubscriptionConfig{
			UserAgent:            "Haruki-Toolbox-Backend",
			RequestTimeoutSecond: 5,
		},
	}
}

func normalizeConfigDefaults(cfg *Config) error {
	if cfg.Backend.ShutdownTimeout <= 0 {
		cfg.Backend.ShutdownTimeout = 10
	}
	if cfg.UserSystem.SMTP.TimeoutSeconds <= 0 {
		cfg.UserSystem.SMTP.TimeoutSeconds = 10
	}
	switch strings.ToLower(strings.TrimSpace(cfg.UserSystem.AuthProvider)) {
	case "", "kratos":
		cfg.UserSystem.AuthProvider = "kratos"
	default:
		return fmt.Errorf("invalid user_system.auth_provider %q", strings.TrimSpace(cfg.UserSystem.AuthProvider))
	}
	if cfg.UserSystem.KratosRequestTimeout <= 0 {
		cfg.UserSystem.KratosRequestTimeout = 10
	}
	if strings.TrimSpace(cfg.UserSystem.AuthProxyTrustedHeader) == "" {
		cfg.UserSystem.AuthProxyTrustedHeader = "X-Auth-Proxy-Secret"
	}
	if strings.TrimSpace(cfg.UserSystem.AuthProxySubjectHeader) == "" {
		cfg.UserSystem.AuthProxySubjectHeader = "X-Kratos-Identity-Id"
	}
	if strings.TrimSpace(cfg.UserSystem.AuthProxyNameHeader) == "" {
		cfg.UserSystem.AuthProxyNameHeader = "X-User-Name"
	}
	if strings.TrimSpace(cfg.UserSystem.AuthProxyEmailHeader) == "" {
		cfg.UserSystem.AuthProxyEmailHeader = "X-User-Email"
	}
	if strings.TrimSpace(cfg.UserSystem.AuthProxyEmailVerifiedHeader) == "" {
		cfg.UserSystem.AuthProxyEmailVerifiedHeader = "X-User-Email-Verified"
	}
	if strings.TrimSpace(cfg.UserSystem.AuthProxyUserIDHeader) == "" {
		cfg.UserSystem.AuthProxyUserIDHeader = "X-User-Id"
	}
	if strings.TrimSpace(cfg.UserSystem.KratosSessionHeader) == "" {
		cfg.UserSystem.KratosSessionHeader = "X-Session-Token"
	}
	if strings.TrimSpace(cfg.UserSystem.KratosSessionCookie) == "" {
		cfg.UserSystem.KratosSessionCookie = "ory_kratos_session"
	}
	if strings.TrimSpace(cfg.OAuth2.Provider) == "" {
		cfg.OAuth2.Provider = "hydra"
	}
	if cfg.OAuth2.HydraRequestTimeoutSecond <= 0 {
		cfg.OAuth2.HydraRequestTimeoutSecond = 10
	}
	if strings.TrimSpace(cfg.Subscription.UserAgent) == "" {
		cfg.Subscription.UserAgent = "Haruki-Toolbox-Backend"
	}
	if cfg.Subscription.RequestTimeoutSecond <= 0 {
		cfg.Subscription.RequestTimeoutSecond = 5
	}
	if strings.TrimSpace(cfg.Afdian.APIBaseURL) == "" {
		cfg.Afdian.APIBaseURL = "https://afdian.com/api/open"
	}
	if cfg.Afdian.RequestTimeoutSecond <= 0 {
		cfg.Afdian.RequestTimeoutSecond = 10
	}
	if cfg.Afdian.SyncIntervalSeconds <= 0 {
		cfg.Afdian.SyncIntervalSeconds = 300
	}
	if cfg.Afdian.SyncIntervalSeconds < 60 {
		cfg.Afdian.SyncIntervalSeconds = 60
	}

	return nil
}
