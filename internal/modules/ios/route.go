package ios

import harukiAPIHelper "haruki-suite/utils/api"

func RegisterIOSRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/ios")

	api.Get("/module/:upload_code/*", handleModuleGeneration(apiHelper))
	api.Get("/script/:upload_code/haruki-toolbox.js", handleScriptGeneration(apiHelper))
}
