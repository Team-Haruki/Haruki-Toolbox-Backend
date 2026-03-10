package admin

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := apiHelper.Router.Group("/api/admin", apiHelper.SessionHandler.VerifySessionToken)
	registerAdminSelfRoutes(apiHelper, adminGroup)
	registerAdminConfigRoutes(apiHelper, adminGroup)
}

func registerAdminConfigRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	cfg := adminGroup.Group("/config", adminCoreModule.RequireSuperAdmin(apiHelper))
	cfg.Get("/public-api-keys", handleGetPublicAPIAllowedKeys(apiHelper))
	cfg.Put("/public-api-keys", handleUpdatePublicAPIAllowedKeys(apiHelper))
	cfg.Get("/runtime", handleGetRuntimeConfig(apiHelper))
	cfg.Put("/runtime", handleUpdateRuntimeConfig(apiHelper))
}

func registerAdminSelfRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	me := adminGroup.Group("/me", adminCoreModule.RequireAdmin(apiHelper))
	me.Get("/sessions", handleListAdminSessions(apiHelper))
	me.Delete("/sessions/:session_token_id", handleDeleteAdminSession(apiHelper))
	me.Post("/reauth", handleAdminReauth(apiHelper))
}
