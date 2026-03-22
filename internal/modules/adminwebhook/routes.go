package adminwebhook

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminWebhookRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	webhooks := adminGroup.Group("/webhooks", adminCoreModule.RequireAdmin(apiHelper))

	webhooks.Get("", handleListAdminWebhooks(apiHelper))
	webhooks.Get("/settings", handleGetAdminWebhookSettings(apiHelper))
	webhooks.Get("/:webhook_id/subscribers", handleListAdminWebhookSubscribers(apiHelper))

	webhooks.Post("", adminCoreModule.RequireSuperAdmin(apiHelper), handleCreateAdminWebhook(apiHelper))
	webhooks.Put("/settings", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateAdminWebhookSettings(apiHelper))
	webhooks.Put("/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateAdminWebhook(apiHelper))
	webhooks.Delete("/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteAdminWebhook(apiHelper))
}
