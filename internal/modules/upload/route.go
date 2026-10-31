package upload

import (
	"context"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/inheritvault"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiDataHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// Dependencies contains the process-level collaborators consumed by upload.
// They are supplied by the composition root instead of being hidden on the
// compatibility RouterHelpers service locator.
type Dependencies struct {
	ValidateUploadIdentity    func(context.Context) error
	HarukiProxyV3ClientPolicy *harukiDataHandler.ClientPolicy
	DataSync                  harukiDataHandler.DataSyncConfig
	BackgroundTasks           harukiBackground.Runner
	OAuth2WebhookAuthorizer   harukiDataHandler.OAuth2WebhookAuthorizer
	HTTPClient                *harukiHttp.Client
	DataHandlerLogger         *harukiLogger.Logger
	BirthdaySubscription      harukiDataHandler.BirthdaySubscriptionConfig
	SuiteRestoreService       *harukiDataHandler.SuiteRestoreService
	ServerCryptor             harukiSekai.ServerCryptor
	Proxy                     string
	// SekaiClient holds the game endpoints, headers and inherit signing
	// secrets used by inherit jobs.
	SekaiClient harukiConfig.SekaiClientConfig
	// InheritPacing sets the pauses between game API calls of an inherit;
	// unset fields keep the defaults.
	InheritPacing harukiSekai.InheritPacing
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
	// InheritVault stores what inherit jobs hand back to the owner. When it
	// has no master key the inherit job and vault endpoints answer 503.
	InheritVault *inheritvault.Store
	// InternalUpload gates the internal upload endpoint; its zero value
	// leaves the route unregistered.
	InternalUpload InternalUploadConfig
}

func RegisterUploadRoutes(apiHelper *apiHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	registerInheritRoutes(apiHelper, dependencies)
	registerIOSUploadRoutes(apiHelper, dependencies)
	registerHarukiProxyRoutes(apiHelper, dependencies)
	registerManualUploadRoutes(apiHelper, dependencies)
	registerOAuth2UploadRoutes(apiHelper, dependencies)
	registerInternalUploadRoutes(apiHelper, dependencies)
}
