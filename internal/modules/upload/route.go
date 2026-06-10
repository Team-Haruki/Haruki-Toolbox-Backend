package upload

import apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

func RegisterUploadRoutes(apiHelper *apiHelper.HarukiToolboxRouterHelpers) {
	registerInheritRoutes(apiHelper)
	registerIOSUploadRoutes(apiHelper)
	registerHarukiProxyRoutes(apiHelper)
	registerManualUploadRoutes(apiHelper)
}
