package admingamebindings

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminGlobalGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := apiHelper.Router.Group("/api/admin", apiHelper.SessionHandler.VerifySessionToken)
	gameBindings := adminGroup.Group("/game-account-bindings", adminCoreModule.RequireAdmin(apiHelper))
	gameBindings.Get("", handleAdminListGlobalGameAccountBindings(apiHelper))
	gameBindings.Post("/batch-delete", handleAdminBatchDeleteGlobalGameAccountBindings(apiHelper))
	gameBindings.Post("/batch-reassign", handleAdminBatchReassignGlobalGameAccountBindings(apiHelper))
	gameBindings.Put("/:server/:game_user_id/reassign", handleAdminReassignGlobalGameAccountBinding(apiHelper))
	gameBindings.Delete("/:server/:game_user_id", handleAdminDeleteGlobalGameAccountBinding(apiHelper))
}
