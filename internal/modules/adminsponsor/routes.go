package adminsponsor

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminSponsorRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router, afdianConfig sharedSponsor.AfdianConfig) {
	sponsors := adminGroup.Group("/sponsors", adminCoreModule.RequireAdmin(apiHelper))

	sponsors.Get("", handleAdminListSponsors(apiHelper))
	sponsors.Post("", handleAdminCreateSponsor(apiHelper))
	sponsors.Get("/:sponsor_id", handleAdminGetSponsor(apiHelper))
	sponsors.Put("/:sponsor_id", handleAdminUpdateSponsor(apiHelper))
	sponsors.Post("/:sponsor_id/manual-durations", handleAdminCreateManualDuration(apiHelper))
	sponsors.Put("/:sponsor_id/manual-durations/:entry_id", handleAdminUpdateManualDuration(apiHelper))
	sponsors.Delete("/:sponsor_id/manual-durations/:entry_id", handleAdminDeleteManualDuration(apiHelper))
	sponsors.Post("/sync/afdian", adminCoreModule.RequireSuperAdmin(apiHelper), handleAdminSyncAfdianSponsors(apiHelper, afdianConfig))
}
