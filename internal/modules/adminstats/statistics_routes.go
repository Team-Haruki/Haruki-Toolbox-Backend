package adminstats

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminStatisticsRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	statistics := adminGroup.Group("/statistics", adminCoreModule.RequireAdmin(apiHelper))
	statistics.Get("/dashboard", handleGetDashboardStatistics(apiHelper))
	statistics.Get("/upload-logs", handleQueryUploadLogs(apiHelper))
	statistics.Get("/timeseries", handleGetStatisticsTimeseries(apiHelper))
}
