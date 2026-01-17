package user

import "haruki-suite/utils/api"

func RegisterUserSystemRoutes(helper *api.HarukiToolboxRouterHelpers) {
	registerEmailRoutes(helper)
	registerLoginRoutes(helper)
	registerAccountRoutes(helper)
	registerGetInfoRoutes(helper)
	registerRegisterRoutes(helper)
	registerPrivateAPIRoutes(helper)
	registerResetPasswordRoutes(helper)
	registerSocialPlatformRoutes(helper)
	registerGameAccountBindingRoutes(helper)
	registerAuthorizeSocialPlatformRoutes(helper)
	registerIOSUploadCodeRoutes(helper)
}
