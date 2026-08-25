package upload

import (
	apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiDataHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
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
	// HydraConfig gates the delegated OAuth2 upload route. When nil that route
	// is not registered at all, so a deployment without Hydra simply does not
	// expose it.
	HydraConfig *harukiOAuth2.HydraConfig
	// OAuth2ClientActiveChecker rejects a token whose client has been disabled.
	// Supplied by the composition root, like OAuth2WebhookAuthorizer, because
	// resolving it lives in the oauth2 module. It is NOT optional: a token
	// outlives the client that minted it, so skipping this check would let a
	// disabled integration keep writing.
	OAuth2ClientActiveChecker harukiOAuth2.ClientActiveChecker
}

func RegisterUploadRoutes(apiHelper *apiHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	registerInheritRoutes(apiHelper, dependencies)
	registerIOSUploadRoutes(apiHelper, dependencies)
	registerHarukiProxyRoutes(apiHelper, dependencies)
	registerManualUploadRoutes(apiHelper, dependencies)
	registerOAuth2UploadRoutes(apiHelper, dependencies)
}
