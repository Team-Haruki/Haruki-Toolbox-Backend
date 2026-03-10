package adminsyslog

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminSystemLogRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	systemLogs := adminGroup.Group("/system-logs", requireAdmin(apiHelper))
	systemLogs.Get("", handleQuerySystemLogs(apiHelper))
	systemLogs.Get("/summary", handleGetSystemLogSummary(apiHelper))
	systemLogs.Get("/export", handleExportSystemLogs(apiHelper))
	systemLogs.Get("/:id", handleGetSystemLogDetail(apiHelper))
}
