package userauth

import harukiAPIHelper "haruki-suite/utils/api"

func RegisterUserAuthRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesManagedBrowserAuth() {
		disabled := LegacyAuthDisabledHandler()
		apiHelper.Router.Post("/api/user/login", disabled)
		apiHelper.Router.Post("/api/user/register", disabled)
		return
	}
	apiHelper.Router.Post("/api/user/login", handleLogin(apiHelper))
	apiHelper.Router.Post("/api/user/register", handleRegister(apiHelper))
}
