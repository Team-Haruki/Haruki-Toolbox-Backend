package misc

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
)

func RegisterMiscRoutes(
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	assets AssetsConfig,
	suiteRestoreService *harukiHandler.SuiteRestoreService,
) {
	registerFriendGroupsRoutes(apiHelper, assets)
	registerFriendLinksRoutes(apiHelper, assets)
	apiHelper.Router.Get("/api/health", handleHealth(apiHelper, suiteRestoreService))
}
