package misc

import harukiAPIHelper "haruki-suite/utils/api"

func RegisterMiscRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	registerFriendGroupsRoutes(apiHelper)
	registerFriendLinksRoutes(apiHelper)
	registerHealthRoutes(apiHelper.Router)
}
