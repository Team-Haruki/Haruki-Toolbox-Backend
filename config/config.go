package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var envPlaceholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

type RestoreSuiteConfig struct {
	EnableRegions  []string          `yaml:"enable_regions"`
	StructuresFile map[string]string `yaml:"structures_file"`
}

type MongoDBConfig struct {
	URL                 string `yaml:"url"`
	DB                  string `yaml:"db"`
	Suite               string `yaml:"suite"`
	Mysekai             string `yaml:"mysekai"`
	PrivateApiSecret    string `yaml:"private_api_secret"`
	PrivateApiUserAgent string `yaml:"private_api_user_agent"`
}

type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
}

type WebhookConfig struct {
	JWTSecret string `yaml:"jwt_secret"`
	Enabled   bool   `yaml:"enabled"`
}

type ThirdPartyDataProviderConfig struct {
	Endpoint8823            string `yaml:"endpoint_8823"`
	Secret8823              string `yaml:"secret_8823"`
	SendJSONZstandard8823   bool   `yaml:"send_json_zstandard_8823"`
	CheckEnabled8823        bool   `yaml:"check_enabled_8823"`
	CheckURL8823            string `yaml:"check_url_8823"`
	RestoreSuite8823        bool   `yaml:"restore_suite_8823"`
	EndpointSakura          string `yaml:"endpoint_sakura"`
	SecretSakura            string `yaml:"secret_sakura"`
	SendJSONZstandardSakura bool   `yaml:"send_json_zstandard_sakura"`
	CheckEnabledSakura      bool   `yaml:"check_enabled_sakura"`
	CheckURLSakura          string `yaml:"check_url_sakura"`
	RestoreSuiteSakura      bool   `yaml:"restore_suite_sakura"`
	EndpointResona          string `yaml:"endpoint_resona"`
	SecretResona            string `yaml:"secret_resona"`
	SendJSONZstandardResona bool   `yaml:"send_json_zstandard_resona"`
	CheckEnabledResona      bool   `yaml:"check_enabled_resona"`
	CheckURLResona          string `yaml:"check_url_resona"`
	RestoreSuiteResona      bool   `yaml:"restore_suite_resona"`
	EndpointLuna            string `yaml:"endpoint_luna"`
	SecretLuna              string `yaml:"secret_luna"`
	SendJSONZstandardLuna   bool   `yaml:"send_json_zstandard_luna"`
	CheckEnabledLuna        bool   `yaml:"check_enabled_luna"`
	CheckURLLuna            string `yaml:"check_url_luna"`
	RestoreSuiteLuna        bool   `yaml:"restore_suite_luna"`
}

type UserSystemConfig struct {
	DBType                       string     `yaml:"db_type"`
	DBURL                        string     `yaml:"db_url"`
	CloudflareSecret             string     `yaml:"cloudflare_secret"`
	TurnstileBypass              bool       `yaml:"turnstile_bypass"`
	SMTP                         SMTPConfig `yaml:"smtp"`
	SessionSignToken             string     `yaml:"session_sign_token"`
	AuthProvider                 string     `yaml:"auth_provider"`
	AuthProxyEnabled             bool       `yaml:"auth_proxy_enabled"`
	AuthProxyTrustedHeader       string     `yaml:"auth_proxy_trusted_header"`
	AuthProxyTrustedValue        string     `yaml:"auth_proxy_trusted_value"`
	AuthProxySubjectHeader       string     `yaml:"auth_proxy_subject_header"`
	AuthProxyNameHeader          string     `yaml:"auth_proxy_name_header"`
	AuthProxyEmailHeader         string     `yaml:"auth_proxy_email_header"`
	AuthProxyEmailVerifiedHeader string     `yaml:"auth_proxy_email_verified_header"`
	AuthProxyUserIDHeader        string     `yaml:"auth_proxy_user_id_header"`
	AuthProxySessionHeader       string     `yaml:"auth_proxy_session_header"`
	KratosPublicURL              string     `yaml:"kratos_public_url"`
	KratosAdminURL               string     `yaml:"kratos_admin_url"`
	KratosRequestTimeout         int        `yaml:"kratos_request_timeout_seconds"`
	KratosSessionHeader          string     `yaml:"kratos_session_header"`
	KratosSessionCookie          string     `yaml:"kratos_session_cookie"`
	KratosAutoLinkByEmail        bool       `yaml:"kratos_auto_link_by_email"`
	KratosAutoProvisionUser      bool       `yaml:"kratos_auto_provision_user"`
	AvatarSaveDir                string     `yaml:"avatar_save_dir"`
	AvatarURL                    string     `yaml:"avatar_url"`
	FrontendURL                  string     `yaml:"frontend_url"`
	SocialPlatformVerifyToken    string     `yaml:"social_platform_verify_token"`
}

type BackendConfig struct {
	Host             string   `yaml:"host"`
	Port             int      `yaml:"port"`
	SSL              bool     `yaml:"ssl"`
	SSLCert          string   `yaml:"ssl_cert"`
	SSLKey           string   `yaml:"ssl_key"`
	AutoMigrate      bool     `yaml:"auto_migrate"`
	ShutdownTimeout  int      `yaml:"shutdown_timeout_seconds"`
	LogLevel         string   `yaml:"log_level"`
	MainLogFile      string   `yaml:"main_log_file"`
	AccessLog        string   `yaml:"access_log"`
	AccessLogPath    string   `yaml:"access_log_path"`
	CSPConnectSrc    []string `yaml:"csp_connect_src"`
	EnableTrustProxy bool     `yaml:"enable_trust_proxy"`
	TrustProxies     []string `yaml:"trusted_proxies"`
	ProxyHeader      string   `yaml:"proxy_header"`
	BackendURL       string   `yaml:"backend_url"`
	BackendCDNURL    string   `yaml:"backend_cdn_url"`
}

type SMTPConfig struct {
	SMTPAddr       string `yaml:"smtp_addr"`
	SMTPPort       int    `yaml:"smtp_port"`
	SMTPMail       string `yaml:"smtp_mail"`
	SMTPPass       string `yaml:"smtp_pass"`
	MailName       string `yaml:"mail_name"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type HarukiBotConfig struct {
	DBURL               string `yaml:"db_url"`
	EnableRegistration  bool   `yaml:"enable_registration"`
	CredentialSignToken string `yaml:"credential_sign_token"`
}

type HarukiProxyConfig struct {
	UserAgent string `yaml:"user_agent"`
	Version   string `yaml:"version"`
	Secret    string `yaml:"secret"`
	UnpackKey string `yaml:"unpack_key"`
}

type SekaiClientConfig struct {
	ENServerAPIHost              string            `yaml:"en_server_api_host"`
	ENServerAESKey               string            `yaml:"en_server_aes_key"`
	ENServerAESIV                string            `yaml:"en_server_aes_iv"`
	JPServerAPIHost              string            `yaml:"jp_server_api_host"`
	TWServerAPIHost              string            `yaml:"tw_server_api_host"`
	TWServerAPIHost2             string            `yaml:"tw_server_api_host_2"`
	KRServerAPIHost              string            `yaml:"kr_server_api_host"`
	KRServerAPIHost2             string            `yaml:"kr_server_api_host_2"`
	CNServerAPIHost              string            `yaml:"cn_server_api_host"`
	CNServerAPIHost2             string            `yaml:"cn_server_api_host_2"`
	OtherServerAESKey            string            `yaml:"other_server_aes_key"`
	OtherServerAESIV             string            `yaml:"other_server_aes_iv"`
	JPServerInheritToken         string            `yaml:"jp_server_inherit_token"`
	ENServerInheritToken         string            `yaml:"en_server_inherit_token"`
	JPServerAppVersionUrl        string            `yaml:"jp_server_app_version_url"`
	ENServerAppVersionUrl        string            `yaml:"en_server_app_version_url"`
	JPServerInheritClientHeaders map[string]string `yaml:"jp_server_inherit_client_headers"`
	ENServerInheritClientHeaders map[string]string `yaml:"en_server_inherit_client_headers"`
	SuiteRemoveKeys              []string          `yaml:"suite_remove_keys"`
}

type SekaiAPIConfig struct {
	APIEndpoint string `yaml:"api_endpoint"`
	APIToken    string `yaml:"api_token"`
}

type OthersConfig struct {
	PublicAPIAllowedKeys []string `yaml:"public_api_allowed_keys"`
}

type OAuth2Config struct {
	Provider                  string `yaml:"provider"`
	HydraPublicURL            string `yaml:"hydra_public_url"`
	HydraBrowserURL           string `yaml:"hydra_browser_url"`
	HydraAdminURL             string `yaml:"hydra_admin_url"`
	HydraClientID             string `yaml:"hydra_client_id"`
	HydraClientSecret         string `yaml:"hydra_client_secret"`
	HydraRequestTimeoutSecond int    `yaml:"hydra_request_timeout_seconds"`
}

type Config struct {
	Proxy                  string                       `yaml:"proxy"`
	MongoDB                MongoDBConfig                `yaml:"mongodb"`
	Redis                  RedisConfig                  `yaml:"redis"`
	Webhook                WebhookConfig                `yaml:"webhook"`
	Backend                BackendConfig                `yaml:"backend"`
	UserSystem             UserSystemConfig             `yaml:"user_system"`
	OAuth2                 OAuth2Config                 `yaml:"oauth2"`
	Others                 OthersConfig                 `yaml:"others"`
	SekaiClient            SekaiClientConfig            `yaml:"sekai_client"`
	SekaiAPI               SekaiAPIConfig               `yaml:"sekai_api"`
	HarukiProxy            HarukiProxyConfig            `yaml:"haruki_proxy"`
	ThirdPartyDataProvider ThirdPartyDataProviderConfig `yaml:"third_party_data_provider"`
	RestoreSuite           RestoreSuiteConfig           `yaml:"restore_suite"`
	HarukiBot              HarukiBotConfig              `yaml:"haruki_bot"`
}

var Cfg Config

const defaultConfigFilename = "haruki-suite-configs.yaml"

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

	return nil
}

func applySMTPConnectionURIFallback(cfg *Config) error {
	connectionURI := strings.TrimSpace(os.Getenv("SMTP_CONNECTION_URI"))
	if connectionURI == "" {
		return nil
	}
	parsed, err := url.Parse(connectionURI)
	if err != nil {
		return fmt.Errorf("parse SMTP_CONNECTION_URI: %w", err)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("parse SMTP_CONNECTION_URI: missing smtp host")
	}
	portValue := strings.TrimSpace(parsed.Port())
	if portValue == "" {
		switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
		case "smtps":
			portValue = "465"
		case "smtp":
			portValue = "587"
		default:
			portValue = "25"
		}
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		return fmt.Errorf("parse SMTP_CONNECTION_URI port %q: %w", portValue, err)
	}

	username := ""
	password := ""
	if parsed.User != nil {
		username = strings.TrimSpace(parsed.User.Username())
		if pass, ok := parsed.User.Password(); ok {
			password = strings.TrimSpace(pass)
		}
	}

	if strings.TrimSpace(cfg.UserSystem.SMTP.SMTPAddr) == "" {
		cfg.UserSystem.SMTP.SMTPAddr = host
	}
	if _, hasExplicitSMTPPortEnv := firstEnvValue("SMTP_PORT"); !hasExplicitSMTPPortEnv {
		cfg.UserSystem.SMTP.SMTPPort = port
	}
	if strings.TrimSpace(cfg.UserSystem.SMTP.SMTPMail) == "" {
		switch {
		case username != "":
			cfg.UserSystem.SMTP.SMTPMail = username
		default:
			fromAddress := strings.TrimSpace(os.Getenv("SMTP_FROM_ADDRESS"))
			if fromAddress != "" {
				cfg.UserSystem.SMTP.SMTPMail = fromAddress
			}
		}
	}
	if strings.TrimSpace(cfg.UserSystem.SMTP.SMTPPass) == "" && password != "" {
		cfg.UserSystem.SMTP.SMTPPass = password
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
