package adminsponsor

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterAdminSponsorRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	sponsors := adminGroup.Group("/sponsors", adminCoreModule.RequireAdmin(apiHelper))

	sponsors.Get("", handleAdminListSponsors(apiHelper))
	sponsors.Put("/:sponsor_id", handleAdminUpdateSponsor(apiHelper))
	sponsors.Post("/sync/afdian", adminCoreModule.RequireSuperAdmin(apiHelper), handleAdminSyncAfdianSponsors(apiHelper))
}
