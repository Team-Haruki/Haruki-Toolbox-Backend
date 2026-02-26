package api

import (
	"haruki-suite/api/ios"
	"haruki-suite/api/misc"
	oauth2Routes "haruki-suite/api/oauth2"
	"haruki-suite/api/public"
	"haruki-suite/api/upload"
	"haruki-suite/api/user"
	"haruki-suite/api/webhook"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	misc.RegisterMiscRoutes(apiHelper)
	user.RegisterUserSystemRoutes(apiHelper)
	webhook.RegisterWebhookRoutes(apiHelper)
	public.RegisterPublicAPIRoutes(apiHelper)
	upload.RegisterUploadAPIRoutes(apiHelper)
	ios.RegisterIOSRoutes(apiHelper)
	oauth2Routes.RegisterOAuth2Routes(apiHelper)
	RegisterDebugRoutes(apiHelper)
}
