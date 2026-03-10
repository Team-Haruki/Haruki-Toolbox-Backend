package adminstats

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterAdminStatisticsRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := apiHelper.Router.Group("/api/admin", apiHelper.SessionHandler.VerifySessionToken)
	statistics := adminGroup.Group("/statistics", adminCoreModule.RequireAdmin(apiHelper))
	statistics.Get("/dashboard", handleGetDashboardStatistics(apiHelper))
	statistics.Get("/upload-logs", handleQueryUploadLogs(apiHelper))
	statistics.Get("/timeseries", handleGetStatisticsTimeseries(apiHelper))
}
