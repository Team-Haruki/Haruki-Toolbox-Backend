package misc

import harukiAPIHelper "haruki-suite/utils/api"

func RegisterMiscRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	registerFriendGroupsRoutes(apiHelper)
	registerHealthRoutes(apiHelper.Router)
}
