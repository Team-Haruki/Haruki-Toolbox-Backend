package userauth

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiCloudflare "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
)

func RegisterUserAuthRoutes(
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	turnstileVerifier harukiCloudflare.Verifier,
	userDataBuilder harukiAPIHelper.UserDataBuilder,
) {
	if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesManagedBrowserAuth() {
		disabled := LegacyAuthDisabledHandler()
		apiHelper.Router.Post("/api/user/login", disabled)
		apiHelper.Router.Post("/api/user/register", disabled)
		return
	}
	apiHelper.Router.Post("/api/user/login", handleLogin(apiHelper, turnstileVerifier, userDataBuilder))
	apiHelper.Router.Post("/api/user/register", handleRegister(apiHelper, turnstileVerifier, userDataBuilder))
}
