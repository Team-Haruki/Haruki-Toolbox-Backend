package config

import "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

type RestoreSuiteConfig struct {
	StructuresFile map[string]string `yaml:"structures_file"`
}

// MongoDBConfig retains the legacy YAML namespace for private API credentials only.
// It no longer configures a database connection.
type MongoDBConfig struct {
	PrivateApiSecret    string `yaml:"private_api_secret"`
	PrivateApiUserAgent string `yaml:"private_api_user_agent"`
}

// GameDataConfig configures the dedicated PostgreSQL store for Project Sekai
// suite/mysekai game data. It is a SEPARATE pool from the Ent/user-system one:
// that pool speaks the lib/pq text protocol, and these reads move whole json
// columns as bytes.
// GameDataReadSource selects which datastore serves game-data reads.
type GameDataReadSource string

const (
	// GameDataReadPostgres is the post-cutover source.
	GameDataReadPostgres GameDataReadSource = "postgres"
)

type GameDataConfig struct {
	URL string `yaml:"url"`
	// ReadSource accepts only postgres; retained to reject stale Mongo deployments.
	ReadSource GameDataReadSource `yaml:"read_source"`
	// MaxConns is the pool ceiling. 0 leaves pgx's default (max(4, NumCPU)).
	MaxConns int `yaml:"max_conns"`
	// MinConns is how many connections are opened eagerly. 0 means "same as
	// MaxConns", which is the intended setting: pgx builds connections lazily,
	// so an unwarmed pool reports a spurious EmptyAcquireCount equal to the
	// worker count on the first burst after every restart.
	MinConns int `yaml:"min_conns"`
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
	WebhookSecret        string `yaml:"webhook_secret"`
	SyncEnabled          bool   `yaml:"sync_enabled"`
	SyncIntervalSeconds  int    `yaml:"sync_interval_seconds"`
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
	// ProfilingEnabled turns on opt-in performance instrumentation: a periodic
	// PG pool + Go GC stats sampler and slow-request autopsies. Off by default;
	// safe to flip on during an incident to diagnose resource saturation.
	ProfilingEnabled bool `yaml:"profiling_enabled"`
	// ProfilingIntervalSeconds is how often the stats sampler logs (default 15s).
	ProfilingIntervalSeconds int `yaml:"profiling_interval_seconds"`
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
	V3ClientPolicy *HarukiProxyClientPolicy `yaml:"v3_client_policy"`
	UserAgent      string                   `yaml:"user_agent"`
	Version        string                   `yaml:"version"`
	Secret         string                   `yaml:"secret"`
	UnpackKey      string                   `yaml:"unpack_key"`
}

type SekaiClientConfig struct {
	ENServerAPIHost              string            `yaml:"en_server_api_host"`
	JPServerAPIHost              string            `yaml:"jp_server_api_host"`
	TWServerAPIHost              string            `yaml:"tw_server_api_host"`
	TWServerAPIHost2             string            `yaml:"tw_server_api_host_2"`
	KRServerAPIHost              string            `yaml:"kr_server_api_host"`
	KRServerAPIHost2             string            `yaml:"kr_server_api_host_2"`
	CNServerAPIHost              string            `yaml:"cn_server_api_host"`
	CNServerAPIHost2             string            `yaml:"cn_server_api_host_2"`
	JPServerInheritToken         string            `yaml:"jp_server_inherit_token"`
	ENServerInheritToken         string            `yaml:"en_server_inherit_token"`
	JPServerAppVersionUrl        string            `yaml:"jp_server_app_version_url"`
	ENServerAppVersionUrl        string            `yaml:"en_server_app_version_url"`
	JPServerInheritClientHeaders map[string]string `yaml:"jp_server_inherit_client_headers"`
	ENServerInheritClientHeaders map[string]string `yaml:"en_server_inherit_client_headers"`
}

type SekaiAPIConfig struct {
	APIEndpoint string `yaml:"api_endpoint"`
	APIToken    string `yaml:"api_token"`
}

type OthersConfig struct {
	// AllowedKeys bounds which top-level game-data keys every NON-PRIVATE API
	// may serve. One list, shared by the public API, the OAuth2 game-data
	// endpoint and the owned-account endpoint. The private API is not bound by
	// it — it is an internal surface with no external callers.
	//
	// DeprecatedPublicAPIAllowedKeys is the former name of the same field, still
	// read so an existing config file keeps working; it is folded into
	// AllowedKeys during normalisation.
	AllowedKeys                    []string `yaml:"allowed_keys"`
	DeprecatedPublicAPIAllowedKeys []string `yaml:"public_api_allowed_keys"`
}

type OAuth2Config struct {
	Provider                  string `yaml:"provider"`
	HydraPublicURL            string `yaml:"hydra_public_url"`
	HydraBrowserURL           string `yaml:"hydra_browser_url"`
	HydraAdminURL             string `yaml:"hydra_admin_url"`
	HydraClientID             string `yaml:"hydra_client_id"`
	HydraClientSecret         string `yaml:"hydra_client_secret"`
	HydraRequestTimeoutSecond int    `yaml:"hydra_request_timeout_seconds"`
	// DeviceFlow configures the RFC 8628 device authorization grant.
	DeviceFlow OAuth2DeviceFlowConfig `yaml:"device_flow"`
	// InternalAPI configures POST /internal/oauth2/introspect.
	InternalAPI OAuth2InternalAPIConfig `yaml:"internal_api"`
}

// OAuth2InternalAPIConfig configures the internal token introspection API that
// our own services (Sekai Station) call over the tailnet or the compose
// network. Oathkeeper has no rule for it.
type OAuth2InternalAPIConfig struct {
	// TokenSHA256 is the hex SHA-256 (64 characters) of the internal bearer
	// token; the token itself never goes into backend config. Empty leaves
	// the route unregistered.
	TokenSHA256 string `yaml:"token_sha256"`
}

// OAuth2DeviceFlowConfig is the startup configuration of the device
// authorization grant. It is validated only when Enabled is true; the runtime
// switch oauth2DeviceFlowEnabled must also be on for the flow to answer.
type OAuth2DeviceFlowConfig struct {
	Enabled bool `yaml:"enabled"`
	// ClientAllowlist empty means every client holding the device grant.
	ClientAllowlist []string `yaml:"client_allowlist"`
	// VerificationURL empty means {user_system.frontend_url}/device.
	VerificationURL string `yaml:"verification_url"`
	// HydraIssuerURL empty means oauth2.hydra_browser_url.
	HydraIssuerURL string `yaml:"hydra_issuer_url"`
	// AllowedOrigins empty means [origin(user_system.frontend_url)].
	AllowedOrigins []string `yaml:"allowed_origins"`

	// The user-code alphabet, length and lifetime must equal Hydra's
	// OAUTH2_DEVICE_AUTHORIZATION_USER_CODE_* and TTL_DEVICE_USER_CODE.
	UserCodeCharset string `yaml:"user_code_charset"`
	UserCodeLength  int    `yaml:"user_code_length"`
	// UserCodeTTL is a Go duration, parsed by startup validation.
	UserCodeTTL string `yaml:"user_code_ttl"`

	MinPollIntervalSeconds       int `yaml:"min_poll_interval_seconds"`
	ClaimTTLSeconds              int `yaml:"claim_ttl_seconds"`
	ApproveLeaseSeconds          int `yaml:"approve_lease_seconds"`
	ApprovalTimeoutSeconds       int `yaml:"approval_timeout_seconds"`
	MinRemainingSecondsToApprove int `yaml:"min_remaining_seconds_to_approve"`
	MaxApproveAttempts           int `yaml:"max_approve_attempts"`
	RecordGraceSeconds           int `yaml:"record_grace_seconds"`
	ReaperIntervalSeconds        int `yaml:"reaper_interval_seconds"`
	ReaperGraceSeconds           int `yaml:"reaper_grace_seconds"`

	Limits OAuth2DeviceFlowLimits `yaml:"limits"`
}

// OAuth2DeviceFlowLimits are the device-flow rate limits. Windows are 10
// minutes except the per-day limits.
type OAuth2DeviceFlowLimits struct {
	// Warn-only counters for device/auth.
	AuthAttemptUnknownClientWarnPer10m int `yaml:"auth_attempt_unknown_client_warn_per_10m"`
	AuthAttemptClientWarnMultiplier    int `yaml:"auth_attempt_client_warn_multiplier"`
	// AuthIssuedGlobalPer10m bounds the public and confidential pools together
	// and feeds the brute-force budget check.
	AuthIssuedGlobalPer10m        int `yaml:"auth_issued_global_per_10m"`
	AuthIssuedPublicPer10m        int `yaml:"auth_issued_public_per_10m"`
	AuthIssuedConfidentialPer10m  int `yaml:"auth_issued_confidential_per_10m"`
	AuthIssuedClientDefaultPer10m int `yaml:"auth_issued_client_default_per_10m"`
	LookupUserPer10m              int `yaml:"lookup_user_per_10m"`
	LookupFailUserPer10m          int `yaml:"lookup_fail_user_per_10m"`
	LookupFailUserPerDay          int `yaml:"lookup_fail_user_per_day"`
	LookupFailGlobalPer10m        int `yaml:"lookup_fail_global_per_10m"`
	DecisionUserPerDay            int `yaml:"decision_user_per_day"`
	MaxSlowDown                   int `yaml:"max_slow_down"`
	MaxIntervalSeconds            int `yaml:"max_interval_seconds"`
}

type RestoreMysekaiConfig struct {
	StructuresFile map[string]string `yaml:"structures_file"`
}

type Config struct {
	Crypto                 map[string]utils.CryptoMaterial `yaml:"crypto"`
	MysekaiRestore         RestoreMysekaiConfig            `yaml:"restore_mysekai"`
	Proxy                  string                          `yaml:"proxy"`
	MongoDB                MongoDBConfig                   `yaml:"mongodb"`
	GameData               GameDataConfig                  `yaml:"game_data"`
	Redis                  RedisConfig                     `yaml:"redis"`
	Webhook                WebhookConfig                   `yaml:"webhook"`
	Afdian                 AfdianConfig                    `yaml:"afdian"`
	Backend                BackendConfig                   `yaml:"backend"`
	UserSystem             UserSystemConfig                `yaml:"user_system"`
	OAuth2                 OAuth2Config                    `yaml:"oauth2"`
	Others                 OthersConfig                    `yaml:"others"`
	SekaiClient            SekaiClientConfig               `yaml:"sekai_client"`
	SekaiAPI               SekaiAPIConfig                  `yaml:"sekai_api"`
	HarukiProxy            HarukiProxyConfig               `yaml:"haruki_proxy"`
	ThirdPartyDataProvider ThirdPartyDataProviderConfig    `yaml:"third_party_data_provider"`
	RestoreSuite           RestoreSuiteConfig              `yaml:"restore_suite"`
	HarukiBot              HarukiBotConfig                 `yaml:"haruki_bot"`
	Subscription           SubscriptionConfig              `yaml:"subscription"`
}

var Cfg Config
