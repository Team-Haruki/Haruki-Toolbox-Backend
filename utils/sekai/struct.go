package sekai

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
)

const RequestDataGeneral = "/6O9YhTzP+c8ty/uImK+2w=="
const (
	JP harukiUtils.SupportedInheritUploadServer = "jp"
	EN harukiUtils.SupportedInheritUploadServer = "en"
)

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

var thisProxy = harukiConfig.Cfg.Proxy
var (
	Api = map[harukiUtils.SupportedInheritUploadServer]string{
		JP: fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.JPServerAPIHost),
		EN: fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.ENServerAPIHost),
	}

	Headers = map[harukiUtils.SupportedInheritUploadServer]map[string]string{
		JP: harukiConfig.Cfg.SekaiClient.JPServerInheritClientHeaders,
		EN: harukiConfig.Cfg.SekaiClient.ENServerInheritClientHeaders,
	}

	Version = map[harukiUtils.SupportedInheritUploadServer]string{
		JP: harukiConfig.Cfg.SekaiClient.JPServerAppVersionUrl,
		EN: harukiConfig.Cfg.SekaiClient.ENServerAppVersionUrl,
	}

	InheritJWTToken = map[harukiUtils.SupportedInheritUploadServer]string{
		JP: harukiConfig.Cfg.SekaiClient.JPServerInheritToken,
		EN: harukiConfig.Cfg.SekaiClient.ENServerInheritToken,
	}
)

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

type HarukiSekaiClient struct {
	server          harukiUtils.SupportedInheritUploadServer
	api             string
	versionURL      string
	inherit         harukiUtils.InheritInformation
	headers         map[string]string
	userID          int64
	credential      string
	loginBonus      bool
	isErrorExist    bool
	errorMessage    string
	inheritJWTToken string

	httpClient *harukiHttp.Client
	logger     *harukiLogger.Logger
}
