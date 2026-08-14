package ios

import harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

func RegisterIOSRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, endpoints EndpointConfig) {
	for _, prefix := range []string{"/ios", "/api/ios"} {
		api := apiHelper.Router.Group(prefix)

		api.Get("/module/:upload_code/*", handleModuleGeneration(apiHelper, endpoints))
		api.Get("/script/:upload_code/haruki-toolbox.js", handleScriptGeneration(apiHelper, endpoints))
	}
}
