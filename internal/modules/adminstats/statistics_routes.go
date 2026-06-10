package adminstats

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
)

func RegisterAdminStatisticsRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	statistics := adminGroup.Group("/statistics", adminCoreModule.RequireAdmin(apiHelper))
	statistics.Get("/dashboard", handleGetDashboardStatistics(apiHelper))
	statistics.Get("/upload-logs", handleQueryUploadLogs(apiHelper))
	statistics.Get("/timeseries", handleGetStatisticsTimeseries(apiHelper))
}
