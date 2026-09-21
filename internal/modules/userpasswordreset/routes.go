package userpasswordreset

import (
	userauth "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userauth"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiCloudflare "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
)

func RegisterUserResetPasswordRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, turnstileVerifier harukiCloudflare.Verifier) {
	if apiHelper == nil || apiHelper.Router == nil {
		return
	}

	a := apiHelper.Router.Group("/api/user")
	if apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesManagedBrowserAuth() {
		disabled := userauth.LegacyAuthDisabledHandler()
		a.Post("/reset-password/send", disabled)
		a.Post("/reset-password", disabled)
		return
	}

	a.Post("/reset-password/send", handleSendResetPassword(apiHelper, turnstileVerifier))
	a.Post("/reset-password", handleResetPassword(apiHelper))
}
