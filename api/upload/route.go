package upload

import apiHelper "haruki-suite/utils/api"

func RegisterUploadAPIRoutes(apiHelper *apiHelper.HarukiToolboxRouterHelpers) {
	registerInheritRoutes(apiHelper)
	registerIOSUploadRoutes(apiHelper)
	registerHarukiProxyRoutes(apiHelper)
	registerManualUploadRoutes(apiHelper)
}
