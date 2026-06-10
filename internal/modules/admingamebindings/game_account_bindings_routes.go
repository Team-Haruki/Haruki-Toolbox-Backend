package admingamebindings

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterAdminGlobalGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	gameBindings := adminGroup.Group("/game-account-bindings", adminCoreModule.RequireAdmin(apiHelper))
	gameBindings.Get("", handleAdminListGlobalGameAccountBindings(apiHelper))
	gameBindings.Post("/batch-delete", handleAdminBatchDeleteGlobalGameAccountBindings(apiHelper))
	gameBindings.Post("/batch-reassign", handleAdminBatchReassignGlobalGameAccountBindings(apiHelper))
	gameBindings.Put("/:server/:game_user_id/reassign", handleAdminReassignGlobalGameAccountBinding(apiHelper))
	gameBindings.Delete("/:server/:game_user_id", handleAdminDeleteGlobalGameAccountBinding(apiHelper))
}
