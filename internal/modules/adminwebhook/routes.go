package adminwebhook

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminWebhookRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	webhooks := adminGroup.Group("/webhooks", adminCoreModule.RequireAdmin(apiHelper))

	webhooks.Get("", adminCoreModule.RequireSuperAdmin(apiHelper), handleListAdminWebhooks(apiHelper))
	webhooks.Get("/settings", handleGetAdminWebhookSettings(apiHelper))
	webhooks.Get("/:webhook_id/subscribers", handleListAdminWebhookSubscribers(apiHelper))

	webhooks.Post("", adminCoreModule.RequireSuperAdmin(apiHelper), handleCreateAdminWebhook(apiHelper))
	webhooks.Put("/settings", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateAdminWebhookSettings(apiHelper))
	webhooks.Put("/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateAdminWebhook(apiHelper))
	webhooks.Delete("/:webhook_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteAdminWebhook(apiHelper))
}
