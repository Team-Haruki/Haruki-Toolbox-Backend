package adminrisk

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminRiskRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	risk := adminGroup.Group("/risk", adminCoreModule.RequireAdmin(apiHelper))

	events := risk.Group("/events")
	events.Get("", handleListRiskEvents(apiHelper))
	events.Post("", handleCreateRiskEvent(apiHelper))
	events.Post("/:event_id/resolve", handleResolveRiskEvent(apiHelper))

	rules := risk.Group("/rules")
	rules.Get("", handleListRiskRules(apiHelper))
	rules.Put("", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpsertRiskRules(apiHelper))
}
