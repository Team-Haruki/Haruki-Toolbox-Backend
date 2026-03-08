package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := apiHelper.Router.Group("/api/admin", apiHelper.SessionHandler.VerifySessionToken)
	registerAdminSelfRoutes(apiHelper, adminGroup)
	registerAdminUserRoutes(apiHelper, adminGroup)
	registerAdminGlobalGameAccountBindingRoutes(apiHelper, adminGroup)
	registerAdminOAuthClientRoutes(apiHelper, adminGroup)
	registerAdminConfigRoutes(apiHelper, adminGroup)
	registerAdminStatisticsRoutes(apiHelper, adminGroup)
	registerAdminSystemLogRoutes(apiHelper, adminGroup)
	registerAdminContentRoutes(apiHelper, adminGroup)
	registerAdminRiskRoutes(apiHelper, adminGroup)
	registerAdminTicketRoutes(apiHelper, adminGroup)
}

func registerAdminUserRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	users := adminGroup.Group("/users")
	users.Get("/", RequireAdmin(apiHelper), handleListUsers(apiHelper))
	users.Post("/batch-ban", RequireAdmin(apiHelper), handleBatchBanUsers(apiHelper))
	users.Post("/batch-unban", RequireAdmin(apiHelper), handleBatchUnbanUsers(apiHelper))
	users.Post("/batch-force-logout", RequireAdmin(apiHelper), handleBatchForceLogoutUsers(apiHelper))
	users.Get("/:target_user_id/detail", RequireAdmin(apiHelper), handleGetUserDetail(apiHelper))
	users.Get("/:target_user_id/activity", RequireAdmin(apiHelper), handleGetUserActivity(apiHelper))
	users.Get("/:target_user_id/oauth-authorizations", RequireAdmin(apiHelper), handleListUserOAuthAuthorizations(apiHelper))
	users.Post("/:target_user_id/revoke-oauth", RequireAdmin(apiHelper), handleRevokeUserOAuth(apiHelper))
	users.Put("/:target_user_id/email", RequireAdmin(apiHelper), handleUpdateUserEmail(apiHelper))
	users.Put("/:target_user_id/allow-cn-mysekai", RequireAdmin(apiHelper), handleUpdateUserAllowCNMysekai(apiHelper))
	users.Get("/:target_user_id/game-account-bindings", RequireAdmin(apiHelper), handleListUserGameAccountBindings(apiHelper))
	users.Put("/:target_user_id/game-account-bindings/:server/:game_user_id", RequireAdmin(apiHelper), handleUpsertUserGameAccountBinding(apiHelper))
	users.Delete("/:target_user_id/game-account-bindings/:server/:game_user_id", RequireAdmin(apiHelper), handleDeleteUserGameAccountBinding(apiHelper))
	users.Get("/:target_user_id/social-platform", RequireAdmin(apiHelper), handleGetUserSocialPlatform(apiHelper))
	users.Put("/:target_user_id/social-platform", RequireAdmin(apiHelper), handleUpsertUserSocialPlatform(apiHelper))
	users.Delete("/:target_user_id/social-platform", RequireAdmin(apiHelper), handleClearUserSocialPlatform(apiHelper))
	users.Get("/:target_user_id/authorized-social-platforms", RequireAdmin(apiHelper), handleListUserAuthorizedSocialPlatforms(apiHelper))
	users.Put("/:target_user_id/authorized-social-platforms/:platform_id", RequireAdmin(apiHelper), handleUpsertUserAuthorizedSocialPlatform(apiHelper))
	users.Delete("/:target_user_id/authorized-social-platforms/:platform_id", RequireAdmin(apiHelper), handleDeleteUserAuthorizedSocialPlatform(apiHelper))
	users.Post("/:target_user_id/ios-upload-code/regenerate", RequireAdmin(apiHelper), handleRegenerateUserIOSUploadCode(apiHelper))
	users.Delete("/:target_user_id/ios-upload-code", RequireAdmin(apiHelper), handleClearUserIOSUploadCode(apiHelper))
	users.Put("/:target_user_id/ban", RequireAdmin(apiHelper), handleBanUser(apiHelper))
	users.Put("/:target_user_id/unban", RequireAdmin(apiHelper), handleUnbanUser(apiHelper))
	users.Delete("/:target_user_id", RequireAdmin(apiHelper), handleSoftDeleteUser(apiHelper))
	users.Post("/:target_user_id/restore", RequireAdmin(apiHelper), handleRestoreUser(apiHelper))
	users.Post("/:target_user_id/reset-password", RequireAdmin(apiHelper), handleResetUserPassword(apiHelper))
	users.Post("/:target_user_id/force-logout", RequireAdmin(apiHelper), handleForceLogoutUser(apiHelper))
	users.Get("/:target_user_id/role", RequireAdmin(apiHelper), handleGetUserRole(apiHelper))
	users.Put("/:target_user_id/role", RequireSuperAdmin(apiHelper), handleUpdateUserRole(apiHelper))
}

func registerAdminConfigRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	cfg := adminGroup.Group("/config", RequireSuperAdmin(apiHelper))
	cfg.Get("/public-api-keys", handleGetPublicAPIAllowedKeys(apiHelper))
	cfg.Put("/public-api-keys", handleUpdatePublicAPIAllowedKeys(apiHelper))
	cfg.Get("/runtime", handleGetRuntimeConfig(apiHelper))
	cfg.Put("/runtime", handleUpdateRuntimeConfig(apiHelper))
}

func registerAdminSelfRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	me := adminGroup.Group("/me", RequireAdmin(apiHelper))
	me.Get("/sessions", handleListAdminSessions(apiHelper))
	me.Delete("/sessions/:session_token_id", handleDeleteAdminSession(apiHelper))
	me.Post("/reauth", handleAdminReauth(apiHelper))
}
