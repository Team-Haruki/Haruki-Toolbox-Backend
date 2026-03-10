package adminsyslog

import (
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminSystemLogRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := apiHelper.Router.Group("/api/admin", apiHelper.SessionHandler.VerifySessionToken)
	systemLogs := adminGroup.Group("/system-logs", requireAdmin(apiHelper))
	systemLogs.Get("", handleQuerySystemLogs(apiHelper))
	systemLogs.Get("/summary", handleGetSystemLogSummary(apiHelper))
	systemLogs.Get("/export", handleExportSystemLogs(apiHelper))
	systemLogs.Get("/:id", handleGetSystemLogDetail(apiHelper))
}
