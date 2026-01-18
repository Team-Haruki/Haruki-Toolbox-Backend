package api

import (
	"haruki-suite/api/ios"
	"haruki-suite/api/misc"
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
}
