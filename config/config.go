package config

import (
	harukiLogger "haruki-suite/utils/logger"
	"os"

	"gopkg.in/yaml.v3"
)

type MongoDBConfig struct {
	URL                 string `yaml:"url"`
	DB                  string `yaml:"db"`
	Suite               string `yaml:"suite"`
	Mysekai             string `yaml:"mysekai"`
	Webhook             string `yaml:"webhook"`
	WebhookUser         string `yaml:"webhook_user"`
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
}

type ThirdPartyDataProviderConfig struct {
	Endpoint8823      string `yaml:"endpoint_8823"`
	Secret8823        string `yaml:"secret_8823"`
	EndpointSakura    string `yaml:"endpoint_sakura"`
	SecretSakura      string `yaml:"secret_sakura"`
	EndpointResona    string `yaml:"endpoint_resona"`
	SecretResona      string `yaml:"secret_resona"`
	EndpointLuna      string `yaml:"endpoint_luna"`
	SecretLuna        string `yaml:"secret_luna"`
	SendJSONZstandard bool   `yaml:"send_json_zstandard"`
}

type UserSystemConfig struct {
	DBType                    string     `yaml:"db_type"`
	DBURL                     string     `yaml:"db_url"`
	CloudflareSecret          string     `yaml:"cloudflare_secret"`
	SMTP                      SMTPConfig `yaml:"smtp"`
	SessionSignToken          string     `yaml:"session_sign_token"`
	AvatarSaveDir             string     `yaml:"avatar_save_dir"`
	AvatarURL                 string     `yaml:"avatar_url"`
	FrontendURL               string     `yaml:"frontend_url"`
	SocialPlatformVerifyToken string     `yaml:"social_platform_verify_token"`
}

type BackendConfig struct {
	Host             string   `yaml:"host"`
	Port             int      `yaml:"port"`
	SSL              bool     `yaml:"ssl"`
	SSLCert          string   `yaml:"ssl_cert"`
	SSLKey           string   `yaml:"ssl_key"`
	LogLevel         string   `yaml:"log_level"`
	MainLogFile      string   `yaml:"main_log_file"`
	AccessLog        string   `yaml:"access_log"`
	AccessLogPath    string   `yaml:"access_log_path"`
	AllowCORS        []string `yaml:"allow_cors"`
	CSPConnectSrc    []string `yaml:"csp_connect_src"`
	EnableTrustProxy bool     `yaml:"enable_trust_proxy"`
	TrustProxies     []string `yaml:"trusted_proxies"`
	ProxyHeader      string   `yaml:"proxy_header"`
	BackendURL       string   `yaml:"backend_url"`
	BackendCDNURL    string   `yaml:"backend_cdn_url"`
}

type SMTPConfig struct {
	SMTPAddr string `yaml:"smtp_addr"`
	SMTPPort int    `yaml:"smtp_port"`
	SMTPMail string `yaml:"smtp_mail"`
	SMTPPass string `yaml:"smtp_pass"`
	MailName string `yaml:"mail_name"`
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

type Config struct {
	Proxy                  string                       `yaml:"proxy"`
	MongoDB                MongoDBConfig                `yaml:"mongodb"`
	Redis                  RedisConfig                  `yaml:"redis"`
	Webhook                WebhookConfig                `yaml:"webhook"`
	Backend                BackendConfig                `yaml:"backend"`
	UserSystem             UserSystemConfig             `yaml:"user_system"`
	Others                 OthersConfig                 `yaml:"others"`
	SekaiClient            SekaiClientConfig            `yaml:"sekai_client"`
	SekaiAPI               SekaiAPIConfig               `yaml:"sekai_api"`
	HarukiProxy            HarukiProxyConfig            `yaml:"haruki_proxy"`
	ThirdPartyDataProvider ThirdPartyDataProviderConfig `yaml:"third_party_data_provider"`
}

var Cfg Config

func init() {
	logger := harukiLogger.NewLogger("ConfigLoader", "DEBUG", nil)
	f, err := os.Open("haruki-suite-configs.yaml")
	if err != nil {
		logger.Errorf("Failed to open config file: %v", err)
		os.Exit(1)
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	decoder := yaml.NewDecoder(f)
	if err := decoder.Decode(&Cfg); err != nil {
		logger.Errorf("Failed to parse config: %v", err)
		os.Exit(1)
	}
}
