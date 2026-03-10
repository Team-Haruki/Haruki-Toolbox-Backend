package adminrisk

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminRiskRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	risk := adminGroup.Group("/risk", adminCoreModule.RequireAdmin(apiHelper))

	events := risk.Group("/events")
	events.Get("", handleListRiskEvents(apiHelper))
	events.Post("", handleCreateRiskEvent(apiHelper))
	events.Post("/:event_id/resolve", handleResolveRiskEvent(apiHelper))

	rules := risk.Group("/rules")
	rules.Get("", handleListRiskRules(apiHelper))
	rules.Put("", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpsertRiskRules(apiHelper))
}
