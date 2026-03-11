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

var RequestDataRefresh = map[string]any{
	"refreshableTypes": []string{
		"new_pending_friend_request",
		"user_report_thanks_message",
		"streaming_virtual_live_reward_status",
	},
}

var RequestDataRefreshLogin = map[string]any{
	"refreshableTypes": []string{
		"new_pending_friend_request",
		"login_bonus",
		"user_report_thanks_message",
		"streaming_virtual_live_reward_status",
	},
}

var RequestDataMySekaiRoom = map[string]any{
	"roomProperty": map[string]any{
		"isRSend": 1,
		"values":  map[string]any{},
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
