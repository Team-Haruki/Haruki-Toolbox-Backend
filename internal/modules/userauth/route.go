package userauth

import harukiAPIHelper "haruki-suite/utils/api"

func RegisterUserAuthRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/login", handleLogin(apiHelper))
	apiHelper.Router.Post("/api/user/register", handleRegister(apiHelper))
}
