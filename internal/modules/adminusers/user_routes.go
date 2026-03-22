package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminUserRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	users := adminGroup.Group("/users", adminCoreModule.RequireAdmin(apiHelper))

	users.Get("", handleListUsers(apiHelper))
	users.Post("/batch-ban", handleBatchBanUsers(apiHelper))
	users.Post("/batch-unban", handleBatchUnbanUsers(apiHelper))
	users.Post("/batch-force-logout", handleBatchForceLogoutUsers(apiHelper))
	users.Post("/batch-role", adminCoreModule.RequireSuperAdmin(apiHelper), handleBatchUpdateUserRole(apiHelper))
	users.Post("/batch-allow-cn-mysekai", handleBatchUpdateUserAllowCNMysekai(apiHelper))
	users.Get("/:target_user_id/detail", handleGetUserDetail(apiHelper))
	users.Get("/:target_user_id/activity", handleGetUserActivity(apiHelper))
	users.Get("/:target_user_id/oauth-authorizations", handleListUserOAuthAuthorizations(apiHelper))
	users.Post("/:target_user_id/revoke-oauth", handleRevokeUserOAuth(apiHelper))
	users.Put("/:target_user_id/email", handleUpdateUserEmail(apiHelper))
	users.Put("/:target_user_id/allow-cn-mysekai", handleUpdateUserAllowCNMysekai(apiHelper))
	users.Get("/:target_user_id/game-account-bindings", handleListUserGameAccountBindings(apiHelper))
	users.Put("/:target_user_id/game-account-bindings/:server/:game_user_id", handleUpsertUserGameAccountBinding(apiHelper))
	users.Delete("/:target_user_id/game-account-bindings/:server/:game_user_id", handleDeleteUserGameAccountBinding(apiHelper))
	users.Get("/:target_user_id/social-platform", handleGetUserSocialPlatform(apiHelper))
	users.Put("/:target_user_id/social-platform", handleUpsertUserSocialPlatform(apiHelper))
	users.Delete("/:target_user_id/social-platform", handleClearUserSocialPlatform(apiHelper))
	users.Get("/:target_user_id/authorized-social-platforms", handleListUserAuthorizedSocialPlatforms(apiHelper))
	users.Put("/:target_user_id/authorized-social-platforms/:platform_id", handleUpsertUserAuthorizedSocialPlatform(apiHelper))
	users.Delete("/:target_user_id/authorized-social-platforms/:platform_id", handleDeleteUserAuthorizedSocialPlatform(apiHelper))
	users.Post("/:target_user_id/ios-upload-code/regenerate", handleRegenerateUserIOSUploadCode(apiHelper))
	users.Delete("/:target_user_id/ios-upload-code", handleClearUserIOSUploadCode(apiHelper))
	users.Put("/:target_user_id/ban", handleBanUser(apiHelper))
	users.Put("/:target_user_id/unban", handleUnbanUser(apiHelper))
	users.Delete("/:target_user_id", handleSoftDeleteUser(apiHelper))
	users.Post("/:target_user_id/restore", handleRestoreUser(apiHelper))
	users.Post("/:target_user_id/reset-password", handleResetUserPassword(apiHelper))
	users.Post("/:target_user_id/force-logout", handleForceLogoutUser(apiHelper))
	users.Get("/:target_user_id/role", handleGetUserRole(apiHelper))
	users.Put("/:target_user_id/role", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateUserRole(apiHelper))
}
