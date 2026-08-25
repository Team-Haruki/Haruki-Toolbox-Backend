package admin

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	registerAdminSelfRoutes(apiHelper, adminGroup)
	registerAdminConfigRoutes(apiHelper, adminGroup)
}

func registerAdminConfigRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	cfg := adminGroup.Group("/config", adminCoreModule.RequireSuperAdmin(apiHelper))
	requireReauth := RequireRecentAdminReauth(apiHelper)
	// The route path and the admin JSON member keep their original spelling even
	// though the Go field is now AllowedKeys: both are the wire contract that
	// existing admin clients and the persisted Redis snapshot use.
	cfg.Get("/public-api-keys", handleGetPublicAPIAllowedKeys(apiHelper))
	cfg.Put("/public-api-keys", requireReauth, handleUpdatePublicAPIAllowedKeys(apiHelper))
	// Required by the MongoDB -> PostgreSQL cutover: flipping the read source
	// must purge the game-data cache in the same step, because no cache key
	// records which datastore produced the body it holds.
	cfg.Post("/game-data-cache/purge", requireReauth, handlePurgeGameDataCache(apiHelper))
	cfg.Get("/runtime", handleGetRuntimeConfig(apiHelper))
	cfg.Put("/runtime", requireReauth, handleUpdateRuntimeConfig(apiHelper))
}

func registerAdminSelfRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	me := adminGroup.Group("/me", adminCoreModule.RequireAdmin(apiHelper))
	me.Get("/ticket-notifications", handleGetAdminTicketNotificationPreference(apiHelper))
	me.Put("/ticket-notifications", handleUpdateAdminTicketNotificationPreference(apiHelper))
	me.Post("/reauth", handleAdminReauth(apiHelper))
}
