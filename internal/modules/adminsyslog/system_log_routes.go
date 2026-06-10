package adminsyslog

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterAdminSystemLogRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	systemLogs := adminGroup.Group("/system-logs", requireAdmin(apiHelper))
	systemLogs.Get("", handleQuerySystemLogs(apiHelper))
	systemLogs.Get("/summary", handleGetSystemLogSummary(apiHelper))
	systemLogs.Get("/export", handleExportSystemLogs(apiHelper))
	systemLogs.Get("/:id", handleGetSystemLogDetail(apiHelper))
}
