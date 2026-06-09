package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var envPlaceholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

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
	cfg := Config{
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
		Subscription: SubscriptionConfig{
			UserAgent:            "Haruki-Toolbox-Backend",
			RequestTimeoutSecond: 5,
		},
	}
	if err := yaml.Unmarshal([]byte(expandedContent), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config file %q: %w", path, err)
	}
	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}
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
		return Config{}, fmt.Errorf("invalid user_system.auth_provider %q", strings.TrimSpace(cfg.UserSystem.AuthProvider))
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

	return cfg, nil
}

func expandEnvPreservingUnknown(content string) string {
	return envPlaceholderPattern.ReplaceAllStringFunc(content, func(token string) string {
		key := strings.TrimPrefix(token, "$")
		key = strings.TrimPrefix(key, "{")
		key = strings.TrimSuffix(key, "}")
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return token
	})
}

func applyEnvOverrides(cfg *Config) error {
	overrideString(&cfg.Proxy, "PROXY_URL", "PROXY")

	overrideString(&cfg.MongoDB.URL, "MONGODB_URL")
	overrideString(&cfg.MongoDB.DB, "MONGODB_DB")
	overrideString(&cfg.MongoDB.Suite, "MONGODB_SUITE_COLLECTION")
	overrideString(&cfg.MongoDB.Mysekai, "MONGODB_MYSEKAI_COLLECTION")
	overrideString(&cfg.MongoDB.PrivateApiSecret, "PRIVATE_API_SECRET")
	overrideString(&cfg.MongoDB.PrivateApiUserAgent, "PRIVATE_API_USER_AGENT")

	overrideString(&cfg.Redis.Host, "REDIS_HOST")
	if err := overrideInt(&cfg.Redis.Port, "REDIS_PORT"); err != nil {
		return err
	}
	overrideString(&cfg.Redis.Password, "REDIS_PASSWORD")

	overrideString(&cfg.Webhook.JWTSecret, "WEBHOOK_JWT_SECRET")
	if err := overrideBool(&cfg.Webhook.Enabled, "WEBHOOK_ENABLED"); err != nil {
		return err
	}

	overrideString(&cfg.UserSystem.DBType, "HARUKI_DB_TYPE")
	overrideString(&cfg.UserSystem.DBURL, "HARUKI_DB_URL")
	overrideString(&cfg.UserSystem.CloudflareSecret, "CLOUDFLARE_SECRET")
	if err := overrideBool(&cfg.UserSystem.TurnstileBypass, "TURNSTILE_BYPASS"); err != nil {
		return err
	}
	overrideString(&cfg.UserSystem.SMTP.SMTPAddr, "SMTP_ADDR")
	if err := overrideInt(&cfg.UserSystem.SMTP.SMTPPort, "SMTP_PORT"); err != nil {
		return err
	}
	overrideString(&cfg.UserSystem.SMTP.SMTPMail, "SMTP_MAIL")
	overrideString(&cfg.UserSystem.SMTP.SMTPPass, "SMTP_PASS")
	overrideString(&cfg.UserSystem.SMTP.MailName, "SMTP_FROM_NAME")
	if err := overrideInt(&cfg.UserSystem.SMTP.TimeoutSeconds, "SMTP_TIMEOUT_SECONDS"); err != nil {
		return err
	}
	if err := applySMTPConnectionURIFallback(cfg); err != nil {
		return err
	}
	overrideString(&cfg.UserSystem.SessionSignToken, "SESSION_SIGN_TOKEN")
	overrideString(&cfg.UserSystem.AuthProvider, "AUTH_PROVIDER")
	if err := overrideBool(&cfg.UserSystem.AuthProxyEnabled, "AUTH_PROXY_ENABLED"); err != nil {
		return err
	}
	overrideString(&cfg.UserSystem.AuthProxyTrustedHeader, "AUTH_PROXY_TRUSTED_HEADER")
	overrideString(&cfg.UserSystem.AuthProxyTrustedValue, "AUTH_PROXY_TRUSTED_VALUE", "OATHKEEPER_SHARED_SECRET")
	overrideString(&cfg.UserSystem.AuthProxySubjectHeader, "AUTH_PROXY_SUBJECT_HEADER")
	overrideString(&cfg.UserSystem.AuthProxyNameHeader, "AUTH_PROXY_NAME_HEADER")
	overrideString(&cfg.UserSystem.AuthProxyEmailHeader, "AUTH_PROXY_EMAIL_HEADER")
	overrideString(&cfg.UserSystem.AuthProxyEmailVerifiedHeader, "AUTH_PROXY_EMAIL_VERIFIED_HEADER")
	overrideString(&cfg.UserSystem.AuthProxyUserIDHeader, "AUTH_PROXY_USER_ID_HEADER")
	overrideString(&cfg.UserSystem.AuthProxySessionHeader, "AUTH_PROXY_SESSION_HEADER")
	overrideString(&cfg.UserSystem.KratosPublicURL, "KRATOS_PUBLIC_URL", "KRATOS_PUBLIC_BASE_URL")
	overrideString(&cfg.UserSystem.KratosAdminURL, "KRATOS_ADMIN_URL", "KRATOS_ADMIN_BASE_URL")
	if err := overrideInt(&cfg.UserSystem.KratosRequestTimeout, "KRATOS_REQUEST_TIMEOUT_SECONDS"); err != nil {
		return err
	}
	overrideString(&cfg.UserSystem.KratosSessionHeader, "KRATOS_SESSION_HEADER")
	overrideString(&cfg.UserSystem.KratosSessionCookie, "KRATOS_SESSION_COOKIE")
	if err := overrideBool(&cfg.UserSystem.KratosAutoLinkByEmail, "KRATOS_AUTO_LINK_BY_EMAIL"); err != nil {
		return err
	}
	if err := overrideBool(&cfg.UserSystem.KratosAutoProvisionUser, "KRATOS_AUTO_PROVISION_USER"); err != nil {
		return err
	}
	overrideString(&cfg.UserSystem.AvatarSaveDir, "AVATAR_SAVE_DIR", "AVATAR_STORAGE_PATH")
	overrideString(&cfg.UserSystem.AvatarURL, "AVATAR_URL", "ASSET_PUBLIC_BASE_URL")
	overrideString(&cfg.UserSystem.FrontendURL, "FRONTEND_URL", "FRONTEND_PUBLIC_URL")
	overrideString(&cfg.UserSystem.SocialPlatformVerifyToken, "SOCIAL_PLATFORM_VERIFY_TOKEN")

	overrideString(&cfg.Backend.Host, "BACKEND_HOST")
	if err := overrideInt(&cfg.Backend.Port, "BACKEND_PORT"); err != nil {
		return err
	}
	if err := overrideBool(&cfg.Backend.SSL, "BACKEND_SSL"); err != nil {
		return err
	}
	overrideString(&cfg.Backend.SSLCert, "BACKEND_SSL_CERT")
	overrideString(&cfg.Backend.SSLKey, "BACKEND_SSL_KEY")
	if err := overrideBool(&cfg.Backend.AutoMigrate, "BACKEND_AUTO_MIGRATE"); err != nil {
		return err
	}
	if err := overrideInt(&cfg.Backend.ShutdownTimeout, "BACKEND_SHUTDOWN_TIMEOUT_SECONDS"); err != nil {
		return err
	}
	overrideString(&cfg.Backend.LogLevel, "BACKEND_LOG_LEVEL")
	overrideString(&cfg.Backend.MainLogFile, "BACKEND_MAIN_LOG_FILE")
	overrideString(&cfg.Backend.AccessLog, "BACKEND_ACCESS_LOG")
	overrideString(&cfg.Backend.AccessLogPath, "BACKEND_ACCESS_LOG_PATH")
	if err := overrideCSV(&cfg.Backend.CSPConnectSrc, "BACKEND_CSP_CONNECT_SRC"); err != nil {
		return err
	}
	if err := overrideBool(&cfg.Backend.EnableTrustProxy, "BACKEND_ENABLE_TRUST_PROXY"); err != nil {
		return err
	}
	if err := overrideCSV(&cfg.Backend.TrustProxies, "BACKEND_TRUSTED_PROXIES"); err != nil {
		return err
	}
	overrideString(&cfg.Backend.ProxyHeader, "BACKEND_PROXY_HEADER")
	overrideString(&cfg.Backend.BackendURL, "BACKEND_URL", "BACKEND_PUBLIC_BASE_URL")
	overrideString(&cfg.Backend.BackendCDNURL, "BACKEND_CDN_URL")

	overrideString(&cfg.OAuth2.Provider, "OAUTH2_PROVIDER")
	overrideString(&cfg.OAuth2.HydraPublicURL, "HYDRA_PUBLIC_URL", "HYDRA_PUBLIC_BASE_URL")
	overrideString(&cfg.OAuth2.HydraBrowserURL, "HYDRA_BROWSER_URL", "BACKEND_PUBLIC_BASE_URL")
	overrideString(&cfg.OAuth2.HydraAdminURL, "HYDRA_ADMIN_URL", "HYDRA_ADMIN_BASE_URL")
	overrideString(&cfg.OAuth2.HydraClientID, "HYDRA_CLIENT_ID")
	overrideString(&cfg.OAuth2.HydraClientSecret, "HYDRA_CLIENT_SECRET")
	if err := overrideInt(&cfg.OAuth2.HydraRequestTimeoutSecond, "HYDRA_REQUEST_TIMEOUT_SECONDS"); err != nil {
		return err
	}

	overrideString(&cfg.SekaiAPI.APIEndpoint, "SEKAI_API_ENDPOINT")
	overrideString(&cfg.SekaiAPI.APIToken, "SEKAI_API_TOKEN")

	overrideString(&cfg.HarukiProxy.UserAgent, "HARUKI_PROXY_USER_AGENT")
	overrideString(&cfg.HarukiProxy.Version, "HARUKI_PROXY_VERSION")
	overrideString(&cfg.HarukiProxy.Secret, "HARUKI_PROXY_SECRET")
	overrideString(&cfg.HarukiProxy.UnpackKey, "HARUKI_PROXY_UNPACK_KEY")

	if err := overrideCSV(&cfg.Others.PublicAPIAllowedKeys, "PUBLIC_API_ALLOWED_KEYS"); err != nil {
		return err
	}

	overrideString(&cfg.HarukiBot.DBURL, "HARUKI_BOT_DB_URL")
	if err := overrideBool(&cfg.HarukiBot.EnableRegistration, "HARUKI_BOT_ENABLE_REGISTRATION"); err != nil {
		return err
	}
	overrideString(&cfg.HarukiBot.CredentialSignToken, "HARUKI_BOT_CREDENTIAL_SIGN_TOKEN")

	overrideString(&cfg.Subscription.HMESInternalBaseURL, "HARUKI_HMES_INTERNAL_BASE_URL")
	overrideString(&cfg.Subscription.HMESInternalToken, "HARUKI_HMES_INTERNAL_API_TOKEN")
	overrideString(&cfg.Subscription.UserAgent, "HARUKI_SUBSCRIPTION_USER_AGENT")
	if err := overrideInt(&cfg.Subscription.RequestTimeoutSecond, "HARUKI_SUBSCRIPTION_REQUEST_TIMEOUT_SECONDS"); err != nil {
		return err
	}

	return nil
}

func overrideString(target *string, keys ...string) {
	if value, ok := firstEnvValue(keys...); ok {
		*target = value
	}
}

func overrideInt(target *int, keys ...string) error {
	value, ok := firstEnvValue(keys...)
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid integer env %s=%q", firstEnvKey(keys...), value)
	}
	*target = parsed
	return nil
}

func overrideBool(target *bool, keys ...string) error {
	value, ok := firstEnvValue(keys...)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("invalid boolean env %s=%q", firstEnvKey(keys...), value)
	}
	*target = parsed
	return nil
}

func overrideCSV(target *[]string, keys ...string) error {
	value, ok := firstEnvValue(keys...)
	if !ok {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	*target = result
	return nil
}

func firstEnvValue(keys ...string) (string, bool) {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func firstEnvKey(keys ...string) string {
	for _, key := range keys {
		if strings.TrimSpace(key) != "" {
			return key
		}
	}
	return ""
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
