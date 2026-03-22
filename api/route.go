package api

import (
	adminModule "haruki-suite/internal/modules/admin"
	adminContentModule "haruki-suite/internal/modules/admincontent"
	adminGameBindingsModule "haruki-suite/internal/modules/admingamebindings"
	adminOAuthModule "haruki-suite/internal/modules/adminoauth"
	adminRiskModule "haruki-suite/internal/modules/adminrisk"
	adminStatsModule "haruki-suite/internal/modules/adminstats"
	adminSyslogModule "haruki-suite/internal/modules/adminsyslog"
	adminTicketsModule "haruki-suite/internal/modules/admintickets"
	adminUsersModule "haruki-suite/internal/modules/adminusers"
	adminWebhookModule "haruki-suite/internal/modules/adminwebhook"
	iosModule "haruki-suite/internal/modules/ios"
	miscModule "haruki-suite/internal/modules/misc"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	publicModule "haruki-suite/internal/modules/public"
	uploadModule "haruki-suite/internal/modules/upload"
	userModule "haruki-suite/internal/modules/user"
	userActivityModule "haruki-suite/internal/modules/useractivity"
	userAuthModule "haruki-suite/internal/modules/userauth"
	userAuthorizeSocialModule "haruki-suite/internal/modules/userauthorizesocial"
	userGameBindingsModule "haruki-suite/internal/modules/usergamebindings"
	userInfoModule "haruki-suite/internal/modules/userinfo"
	userOAuthModule "haruki-suite/internal/modules/useroauth"
	userPasswordResetModule "haruki-suite/internal/modules/userpasswordreset"
	userPrivateAPIModule "haruki-suite/internal/modules/userprivateapi"
	userProfileModule "haruki-suite/internal/modules/userprofile"
	userSocialModule "haruki-suite/internal/modules/usersocial"
	userTicketsModule "haruki-suite/internal/modules/usertickets"
	webhookModule "haruki-suite/internal/modules/webhook"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	miscModule.RegisterMiscRoutes(apiHelper)
	registerAdminRoutes(apiHelper)
	registerUserRoutes(apiHelper)
	webhookModule.RegisterWebhookRoutes(apiHelper)
	publicModule.RegisterPublicRoutes(apiHelper)
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
