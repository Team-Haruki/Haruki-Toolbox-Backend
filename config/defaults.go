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
		GameData: GameDataConfig{
			ReadSource: GameDataReadMongo,
			// MaxConns 0 keeps pgx's own default; MinConns 0 is resolved to
			// MaxConns by the pool so it always starts warm.
			MaxConns: 0,
			MinConns: 0,
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
	// `public_api_allowed_keys` was renamed to `allowed_keys` when the list
	// became the single allowlist for every non-private API. Accept the old
	// spelling rather than silently serving an empty allowlist, which would turn
	// every public game-data response into {}.
	if len(cfg.Others.AllowedKeys) == 0 && len(cfg.Others.DeprecatedPublicAPIAllowedKeys) > 0 {
		cfg.Others.AllowedKeys = append([]string(nil), cfg.Others.DeprecatedPublicAPIAllowedKeys...)
	}
	cfg.Others.DeprecatedPublicAPIAllowedKeys = nil

	if cfg.GameData.ReadSource == "" {
		cfg.GameData.ReadSource = GameDataReadMongo
	}
	switch cfg.GameData.ReadSource {
	case GameDataReadMongo, GameDataReadPostgres:
	default:
		return fmt.Errorf("game_data.read_source must be %q or %q, got %q",
			GameDataReadMongo, GameDataReadPostgres, cfg.GameData.ReadSource)
	}
	if cfg.GameData.ReadSource == GameDataReadPostgres && cfg.GameData.URL == "" {
		return fmt.Errorf("game_data.read_source=%q requires game_data.url", GameDataReadPostgres)
	}

	if cfg.Backend.ShutdownTimeout <= 0 {
		cfg.Backend.ShutdownTimeout = 10
	}
	if cfg.Backend.ProfilingIntervalSeconds <= 0 {
		cfg.Backend.ProfilingIntervalSeconds = 15
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
