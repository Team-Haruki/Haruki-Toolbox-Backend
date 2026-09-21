package adminsponsor

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminSponsorRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router, afdianConfig sharedSponsor.AfdianConfig) {
	sponsors := adminGroup.Group("/sponsors", adminCoreModule.RequireAdmin(apiHelper))

	sponsors.Get("", handleAdminListSponsors(apiHelper))
	sponsors.Put("/:sponsor_id", handleAdminUpdateSponsor(apiHelper))
	sponsors.Post("/sync/afdian", adminCoreModule.RequireSuperAdmin(apiHelper), handleAdminSyncAfdianSponsors(apiHelper, afdianConfig))
}
