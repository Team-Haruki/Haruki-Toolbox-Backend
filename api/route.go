package api

import (
	"context"
	adminModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admin"
	adminContentModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincontent"
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	adminGameBindingsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admingamebindings"
	adminOAuthModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminoauth"
	adminRiskModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminrisk"
	adminSponsorModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminsponsor"
	adminStatsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminstats"
	adminSyslogModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminsyslog"
	adminTicketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admintickets"
	adminUsersModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminusers"
	adminWebhookModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminwebhook"
	harukiBotNeoModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/harukibotneo"
	iosModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/ios"
	miscModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/misc"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	publicModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/public"
	sponsorModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	subscriptionModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/subscription"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	uploadModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/upload"
	userModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/user"
	userActivityModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/useractivity"
	userAuthModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userauth"
	userAuthorizeSocialModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userauthorizesocial"
	userGameBindingsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usergamebindings"
	userInfoModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userinfo"
	userOAuthModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/useroauth"
	userPasswordResetModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userpasswordreset"
	userPrivateAPIModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userprivateapi"
	userProfileModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userprofile"
	userSocialModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usersocial"
	userTicketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usertickets"
	webhookModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/webhook"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiCloudflare "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
)

type TurnstileVerifier interface {
	Verify(ctx context.Context, response, remoteIP string) (*harukiCloudflare.TurnstileResponse, error)
}

// Dependencies contains process-level collaborators that are explicitly routed
// to the modules that consume them. Keep this structure narrow; database and
// session access remain on the compatibility helper during their own migrations.
type Dependencies struct {
	BackgroundTasks      harukiBackground.Runner
	TurnstileVerifier    TurnstileVerifier
	UserDataBuilder      harukiAPIHelper.UserDataBuilder
	AfdianConfig         sponsorModule.AfdianConfig
	MiscAssets           miscModule.AssetsConfig
	TicketNotifications  ticketsModule.NotificationConfig
	HydraConfig          *harukiOAuth2.HydraConfig
	IOSEndpoints         iosModule.EndpointConfig
	OAuth2AvatarBaseURL  string
	UserProfileConfig    userProfileModule.Config
	SocialBotVerify      userSocialModule.BotVerifyConfig
	UploadHTTPClient     *harukiHttp.Client
	UploadLogger         *harukiLogger.Logger
	BirthdaySubscription harukiHandler.BirthdaySubscriptionConfig
	SuiteRestoreService  *harukiHandler.SuiteRestoreService
	ServerCryptor        harukiSekai.ServerCryptor
	UploadProxy          string
}

func RegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	miscModule.RegisterMiscRoutes(apiHelper, dependencies.MiscAssets, dependencies.SuiteRestoreService)
	sponsorModule.RegisterSponsorRoutes(apiHelper, dependencies.AfdianConfig)
	registerAdminRoutes(apiHelper, dependencies)
	registerUserRoutes(apiHelper, dependencies)
	harukiBotNeoModule.RegisterHarukiBotNeoRoutes(apiHelper)
	webhookModule.RegisterWebhookRoutes(apiHelper)
	publicModule.RegisterPublicRoutes(apiHelper)
	subscriptionModule.RegisterSubscriptionRoutes(apiHelper)
	uploadModule.RegisterUploadRoutes(apiHelper, uploadModule.Dependencies{
		BackgroundTasks:           dependencies.BackgroundTasks,
		OAuth2WebhookAuthorizer:   oauth2Module.WebhookAuthorizer{HydraConfig: dependencies.HydraConfig},
		HydraConfig:               dependencies.HydraConfig,
		OAuth2ClientActiveChecker: oauth2Module.HydraClientActiveChecker(dependencies.HydraConfig),
		HTTPClient:                dependencies.UploadHTTPClient,
		DataHandlerLogger:         dependencies.UploadLogger,
		BirthdaySubscription:      dependencies.BirthdaySubscription,
		SuiteRestoreService:       dependencies.SuiteRestoreService,
		ServerCryptor:             dependencies.ServerCryptor,
		Proxy:                     dependencies.UploadProxy,
	})
	iosModule.RegisterIOSRoutes(apiHelper, dependencies.IOSEndpoints)
	oauth2Module.RegisterOAuth2Routes(apiHelper, oauth2Module.RouteOptions{
		HydraConfig:   dependencies.HydraConfig,
		AvatarBaseURL: dependencies.OAuth2AvatarBaseURL,
	})
}

func registerAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	adminModule.RegisterAdminRoutes(apiHelper, adminGroup)
	adminUsersModule.RegisterAdminUserRoutes(apiHelper, adminGroup, dependencies.HydraConfig, dependencies.UserDataBuilder)
	adminContentModule.RegisterAdminContentRoutes(apiHelper, adminGroup)
	adminGameBindingsModule.RegisterAdminGlobalGameAccountBindingRoutes(apiHelper, adminGroup)
	adminOAuthModule.RegisterAdminOAuthClientRoutes(apiHelper, adminGroup, dependencies.HydraConfig)
	adminRiskModule.RegisterAdminRiskRoutes(apiHelper, adminGroup)
	adminSponsorModule.RegisterAdminSponsorRoutes(apiHelper, adminGroup, dependencies.AfdianConfig)
	adminSyslogModule.RegisterAdminSystemLogRoutes(apiHelper, adminGroup)
	adminStatsModule.RegisterAdminStatisticsRoutes(apiHelper, adminGroup)
	adminTicketsModule.RegisterAdminTicketRoutes(apiHelper, adminGroup, dependencies.TicketNotifications)
	adminWebhookModule.RegisterAdminWebhookRoutes(apiHelper, adminGroup)
}

func registerUserRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	userModule.RegisterUserRoutes(apiHelper)
	userInfoModule.RegisterUserInfoRoutes(apiHelper, dependencies.UserDataBuilder)
	userPrivateAPIModule.RegisterUserPrivateAPIRoutes(apiHelper)
	userAuthModule.RegisterUserAuthRoutes(apiHelper, dependencies.TurnstileVerifier, dependencies.UserDataBuilder)
	userPasswordResetModule.RegisterUserResetPasswordRoutes(apiHelper, dependencies.TurnstileVerifier)
	userProfileModule.RegisterUserProfileRoutes(apiHelper, dependencies.UserProfileConfig)
	userOAuthModule.RegisterUserOAuthAuthorizationRoutes(apiHelper, dependencies.HydraConfig)
	userAuthorizeSocialModule.RegisterUserAuthorizeSocialRoutes(apiHelper)
	userSocialModule.RegisterUserSocialRoutes(apiHelper, dependencies.TurnstileVerifier, dependencies.SocialBotVerify)
	userGameBindingsModule.RegisterUserGameAccountBindingRoutes(apiHelper)
	userActivityModule.RegisterUserActivityLogRoutes(apiHelper)
	userTicketsModule.RegisterUserTicketRoutes(apiHelper, dependencies.TicketNotifications)
}
