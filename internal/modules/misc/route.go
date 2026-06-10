package misc

import harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

func RegisterMiscRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	registerFriendGroupsRoutes(apiHelper)
	registerFriendLinksRoutes(apiHelper)
	apiHelper.Router.Get("/api/health", handleHealth(apiHelper))
}
