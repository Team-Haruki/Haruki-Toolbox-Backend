package api

import (
	"haruki-suite/api/public"
	"haruki-suite/api/upload"
	"haruki-suite/api/user"
	"haruki-suite/api/webhook"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	user.RegisterUserSystemRoutes(apiHelper)
	webhook.RegisterWebhookRoutes(apiHelper)
	public.RegisterPublicAPIRoutes(apiHelper)
	upload.RegisterUploadAPIRoutes(apiHelper)
}
