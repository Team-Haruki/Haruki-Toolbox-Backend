package userinfo

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterUserInfoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Get("/api/user/me", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleGetMe(apiHelper))
	apiHelper.Router.Get("/api/user/:toolbox_user_id/get-settings", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleGetSettings(apiHelper))
	registerGameDataRoutes(apiHelper)
}
