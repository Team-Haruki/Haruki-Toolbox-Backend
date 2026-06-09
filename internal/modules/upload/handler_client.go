package upload

import (
	harukiConfig "haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiDataHandler "haruki-suite/utils/handler"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
	"strings"
	"sync"
	"time"
)

var (
	sharedHttpClient        *harukiHttp.Client
	sharedHttpClientProxy   string
	sharedHttpClientMu      sync.RWMutex
	sharedDataHandlerLogger = harukiLogger.NewLoggerFromGlobal("SekaiDataHandler")
)

func newUploadDataHandler(helper *harukiAPIHelper.HarukiToolboxRouterHelpers) *harukiDataHandler.DataHandler {
	return &harukiDataHandler.DataHandler{
		DBManager:      helper.DBManager,
		SekaiAPIClient: helper.SekaiAPIClient,
		HttpClient:     getSharedHTTPClient(),
		Logger:         sharedDataHandlerLogger,
		WebhookEnabled: helper.GetWebhookEnabled(),
	}
}

func getSharedHTTPClient() *harukiHttp.Client {
	proxy := strings.TrimSpace(harukiConfig.Cfg.Proxy)

	sharedHttpClientMu.RLock()
	if sharedHttpClient != nil && sharedHttpClientProxy == proxy {
		client := sharedHttpClient
		sharedHttpClientMu.RUnlock()
		return client
	}
	sharedHttpClientMu.RUnlock()

	client := harukiHttp.NewClient(proxy, 15*time.Second)

	sharedHttpClientMu.Lock()
	defer sharedHttpClientMu.Unlock()
	if sharedHttpClient == nil || sharedHttpClientProxy != proxy {
		sharedHttpClient = client
		sharedHttpClientProxy = proxy
	}
	return sharedHttpClient
}
