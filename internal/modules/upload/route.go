package upload

import (
	apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiDataHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
)

// Dependencies contains the process-level collaborators consumed by upload.
// They are supplied by the composition root instead of being hidden on the
// compatibility RouterHelpers service locator.
type Dependencies struct {
	BackgroundTasks         harukiBackground.Runner
	OAuth2WebhookAuthorizer harukiDataHandler.OAuth2WebhookAuthorizer
	HTTPClient              *harukiHttp.Client
	DataHandlerLogger       *harukiLogger.Logger
	BirthdaySubscription    harukiDataHandler.BirthdaySubscriptionConfig
	SuiteRestoreService     *harukiDataHandler.SuiteRestoreService
	ServerCryptor           harukiSekai.ServerCryptor
	Proxy                   string
}

func RegisterUploadRoutes(apiHelper *apiHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	registerInheritRoutes(apiHelper, dependencies)
	registerIOSUploadRoutes(apiHelper, dependencies)
	registerHarukiProxyRoutes(apiHelper, dependencies)
	registerManualUploadRoutes(apiHelper, dependencies)
}
