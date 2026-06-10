package api

import (
	adminModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admin"
	adminContentModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincontent"
	adminGameBindingsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admingamebindings"
	adminOAuthModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminoauth"
	adminRiskModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/adminrisk"
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
	subscriptionModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/subscription"
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
)

func RegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	miscModule.RegisterMiscRoutes(apiHelper)
	registerAdminRoutes(apiHelper)
	registerUserRoutes(apiHelper)
	harukiBotNeoModule.RegisterHarukiBotNeoRoutes(apiHelper)
	webhookModule.RegisterWebhookRoutes(apiHelper)
	publicModule.RegisterPublicRoutes(apiHelper)
	subscriptionModule.RegisterSubscriptionRoutes(apiHelper)
	uploadModule.RegisterUploadRoutes(apiHelper)
	iosModule.RegisterIOSRoutes(apiHelper)
	oauth2Module.RegisterOAuth2Routes(apiHelper)
}

func registerAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminModule.RegisterAdminRoutes(apiHelper)
	adminUsersModule.RegisterAdminUserRoutes(apiHelper)
	adminContentModule.RegisterAdminContentRoutes(apiHelper)
	adminGameBindingsModule.RegisterAdminGlobalGameAccountBindingRoutes(apiHelper)
	adminOAuthModule.RegisterAdminOAuthClientRoutes(apiHelper)
	adminRiskModule.RegisterAdminRiskRoutes(apiHelper)
	adminSyslogModule.RegisterAdminSystemLogRoutes(apiHelper)
	adminStatsModule.RegisterAdminStatisticsRoutes(apiHelper)
	adminTicketsModule.RegisterAdminTicketRoutes(apiHelper)
	adminWebhookModule.RegisterAdminWebhookRoutes(apiHelper)
}

func registerUserRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	userModule.RegisterUserRoutes(apiHelper)
	userInfoModule.RegisterUserInfoRoutes(apiHelper)
	userPrivateAPIModule.RegisterUserPrivateAPIRoutes(apiHelper)
	userAuthModule.RegisterUserAuthRoutes(apiHelper)
	userPasswordResetModule.RegisterUserResetPasswordRoutes(apiHelper)
	userProfileModule.RegisterUserProfileRoutes(apiHelper)
	userOAuthModule.RegisterUserOAuthAuthorizationRoutes(apiHelper)
	userAuthorizeSocialModule.RegisterUserAuthorizeSocialRoutes(apiHelper)
	userSocialModule.RegisterUserSocialRoutes(apiHelper)
	userGameBindingsModule.RegisterUserGameAccountBindingRoutes(apiHelper)
	userActivityModule.RegisterUserActivityLogRoutes(apiHelper)
	userTicketsModule.RegisterUserTicketRoutes(apiHelper)
}
