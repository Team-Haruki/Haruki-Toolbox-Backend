package misc

import harukiAPIHelper "haruki-suite/utils/api"

func RegisterMiscRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	registerFriendGroupsRoutes(apiHelper)
	registerFriendLinksRoutes(apiHelper)
	apiHelper.Router.Get("/health", handleHealth())
}
