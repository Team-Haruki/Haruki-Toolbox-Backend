package config

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

type AfdianConfig struct {
	UserID               string `yaml:"user_id"`
	APIToken             string `yaml:"api_token"`
	APIBaseURL           string `yaml:"api_base_url"`
	RequestTimeoutSecond int    `yaml:"request_timeout_seconds"`
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

type SubscriptionConfig struct {
	HMESInternalBaseURL  string `yaml:"hmes_internal_base_url"`
	HMESInternalToken    string `yaml:"hmes_internal_token"`
	UserAgent            string `yaml:"user_agent"`
	RequestTimeoutSecond int    `yaml:"request_timeout_seconds"`
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
	Afdian                 AfdianConfig                 `yaml:"afdian"`
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
	Subscription           SubscriptionConfig           `yaml:"subscription"`
}

var Cfg Config
