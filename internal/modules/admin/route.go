package admin

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	registerAdminSelfRoutes(apiHelper, adminGroup)
	registerAdminConfigRoutes(apiHelper, adminGroup)
}

func registerAdminConfigRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	cfg := adminGroup.Group("/config", adminCoreModule.RequireSuperAdmin(apiHelper))
	requireReauth := RequireRecentAdminReauth(apiHelper)
	cfg.Get("/public-api-keys", handleGetPublicAPIAllowedKeys(apiHelper))
	cfg.Put("/public-api-keys", requireReauth, handleUpdatePublicAPIAllowedKeys(apiHelper))
	cfg.Get("/runtime", handleGetRuntimeConfig(apiHelper))
	cfg.Put("/runtime", requireReauth, handleUpdateRuntimeConfig(apiHelper))
}

func registerAdminSelfRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	me := adminGroup.Group("/me", adminCoreModule.RequireAdmin(apiHelper))
	me.Post("/reauth", handleAdminReauth(apiHelper))
}
