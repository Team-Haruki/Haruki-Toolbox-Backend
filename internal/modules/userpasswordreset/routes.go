package userpasswordreset

import (
	userauth "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userauth"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterUserResetPasswordRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
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

	a.Post("/reset-password/send", handleSendResetPassword(apiHelper))
	a.Post("/reset-password", handleResetPassword(apiHelper))
}
