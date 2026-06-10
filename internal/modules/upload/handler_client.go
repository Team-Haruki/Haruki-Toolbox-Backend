package upload

import (
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiDataHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
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
