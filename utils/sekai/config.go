package sekai

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
)

const (
	ServerJP harukiUtils.SupportedInheritUploadServer = "jp"
	ServerEN harukiUtils.SupportedInheritUploadServer = "en"
)

const (
	JP = ServerJP
	EN = ServerEN
)

const RequestDataGeneral = "/6O9YhTzP+c8ty/uImK+2w=="

var RequestDataRefresh = map[string]interface{}{
	"refreshableTypes": []string{
		"new_pending_friend_request",
		"user_report_thanks_message",
		"streaming_virtual_live_reward_status",
	},
}

var RequestDataRefreshLogin = map[string]interface{}{
	"refreshableTypes": []string{
		"new_pending_friend_request",
		"login_bonus",
		"user_report_thanks_message",
		"streaming_virtual_live_reward_status",
	},
}

var RequestDataMySekaiRoom = map[string]interface{}{
	"roomProperty": map[string]interface{}{
		"isRSend": 1,
		"values":  map[string]interface{}{},
	},
}

type ServerConfig struct {
	APIEndpoint     string
	APIHost         string
	Headers         map[string]string
	AppVersionURL   string
	InheritJWTToken string
}

func GetServerConfig(server harukiUtils.SupportedInheritUploadServer) (*ServerConfig, error) {
	cfg := harukiConfig.Cfg.SekaiClient
	switch server {
	case ServerJP:
		return &ServerConfig{
			APIEndpoint:     fmt.Sprintf("https://%s/api", cfg.JPServerAPIHost),
			APIHost:         cfg.JPServerAPIHost,
			Headers:         cfg.JPServerInheritClientHeaders,
			AppVersionURL:   cfg.JPServerAppVersionUrl,
			InheritJWTToken: cfg.JPServerInheritToken,
		}, nil
	case ServerEN:
		return &ServerConfig{
			APIEndpoint:     fmt.Sprintf("https://%s/api", cfg.ENServerAPIHost),
			APIHost:         cfg.ENServerAPIHost,
			Headers:         cfg.ENServerInheritClientHeaders,
			AppVersionURL:   cfg.ENServerAppVersionUrl,
			InheritJWTToken: cfg.ENServerInheritToken,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidServer, server)
	}
}

var proxyURL = harukiConfig.Cfg.Proxy
var Api = map[harukiUtils.SupportedInheritUploadServer]string{
	JP: fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.JPServerAPIHost),
	EN: fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.ENServerAPIHost),
}
var Headers = map[harukiUtils.SupportedInheritUploadServer]map[string]string{
	JP: harukiConfig.Cfg.SekaiClient.JPServerInheritClientHeaders,
	EN: harukiConfig.Cfg.SekaiClient.ENServerInheritClientHeaders,
}
var Version = map[harukiUtils.SupportedInheritUploadServer]string{
	JP: harukiConfig.Cfg.SekaiClient.JPServerAppVersionUrl,
	EN: harukiConfig.Cfg.SekaiClient.ENServerAppVersionUrl,
}
var InheritJWTToken = map[harukiUtils.SupportedInheritUploadServer]string{
	JP: harukiConfig.Cfg.SekaiClient.JPServerInheritToken,
	EN: harukiConfig.Cfg.SekaiClient.ENServerInheritToken,
}
var thisProxy = proxyURL
var acquirePath = map[harukiUtils.UploadDataType]string{
	harukiUtils.UploadDataTypeSuite:                "/suite/user/%d",
	harukiUtils.UploadDataTypeMysekai:              "/user/%d/mysekai",
	harukiUtils.UploadDataTypeMysekaiBirthdayParty: "/user/%d/mysekai/birthday-party/%d/delivery",
}
var allowedHeaders = map[string]struct{}{
	"user-agent":        {},
	"cookie":            {},
	"x-forwarded-for":   {},
	"accept-language":   {},
	"accept":            {},
	"accept-encoding":   {},
	"x-devicemodel":     {},
	"x-app-hash":        {},
	"x-operatingsystem": {},
	"x-kc":              {},
	"x-unity-version":   {},
	"x-app-version":     {},
	"x-platform":        {},
	"x-session-token":   {},
	"x-asset-version":   {},
	"x-request-id":      {},
	"x-data-version":    {},
	"content-type":      {},
	"x-install-id":      {},
}

type Client struct {
	server          harukiUtils.SupportedInheritUploadServer
	api             string
	versionURL      string
	inherit         harukiUtils.InheritInformation
	inheritJWTToken string
	credential      string
	headers         map[string]string
	userID          int64
	loginBonus      bool
	isErrorExist    bool
	errorMessage    string
	httpClient      *harukiHttp.Client
	logger          *harukiLogger.Logger
}

type HarukiSekaiClient = Client
