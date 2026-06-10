package usersocial

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterUserSocialRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	social := apiHelper.Router.Group("/api/user/:toolbox_user_id/social-platform", userCoreModule.RouteHandlers(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"))...)
	requireVerifiedEmail := userCoreModule.RequireVerifiedEmail()

	social.Post("/send-qq-mail", requireVerifiedEmail, handleSendQQMail(apiHelper))
	social.Post("/verify-qq-mail", requireVerifiedEmail, handleVerifyQQMail(apiHelper))
	social.Post("/generate-verification-code", requireVerifiedEmail, handleGenerateVerificationCode(apiHelper))
	social.Get("/verification-status/:status_token", handleVerificationStatus(apiHelper))
	social.Delete("/clear", handleClearSocialPlatform(apiHelper))

	apiHelper.Router.Post("/api/verify-social-platform", handleVerifySocialPlatform(apiHelper))
}
