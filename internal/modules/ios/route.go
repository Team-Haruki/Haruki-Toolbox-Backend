package ios

import harukiAPIHelper "haruki-suite/utils/api"

func RegisterIOSRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	for _, prefix := range []string{"/ios", "/api/ios"} {
		api := apiHelper.Router.Group(prefix)

		api.Get("/module/:upload_code/*", handleModuleGeneration(apiHelper))
		api.Get("/script/:upload_code/haruki-toolbox.js", handleScriptGeneration(apiHelper))
	}
}
