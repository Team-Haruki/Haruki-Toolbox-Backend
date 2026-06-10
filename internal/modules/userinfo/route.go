package userinfo

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterUserInfoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	meHandler, meRest := userCoreModule.RouteHandlerParts(userCoreModule.RequireAuthenticatedUser(apiHelper), handleGetMe(apiHelper))
	apiHelper.Router.Get("/api/user/me", meHandler, meRest...)

	settingsHandler, settingsRest := userCoreModule.RouteHandlerParts(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"), handleGetSettings(apiHelper))
	apiHelper.Router.Get("/api/user/:toolbox_user_id/get-settings", settingsHandler, settingsRest...)
}
