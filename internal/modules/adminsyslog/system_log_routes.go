package adminsyslog

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterAdminSystemLogRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	systemLogs := adminGroup.Group("/system-logs", requireAdmin(apiHelper))
	systemLogs.Get("", handleQuerySystemLogs(apiHelper))
	systemLogs.Get("/summary", handleGetSystemLogSummary(apiHelper))
	systemLogs.Get("/export", handleExportSystemLogs(apiHelper))
	systemLogs.Get("/:id", handleGetSystemLogDetail(apiHelper))
}
