package adminstats

import (
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func handleGetDashboardStatistics(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		windowHours, err := parseStatisticsWindowHours(c.Query("hours"))
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid hours")
		}

		stats, err := buildDashboardStatistics(c, apiHelper, windowHours)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query dashboard statistics")
		}

		return harukiAPIHelper.SuccessResponse(c, "success", stats)
	}
}

func handleGetStatisticsTimeseries(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), adminNow())
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid time range")
		}

		bucket, err := parseStatisticsTimeseriesBucket(c.Query("bucket"))
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid bucket")
		}

		resp, err := buildStatisticsTimeseries(c, apiHelper, from, to, bucket)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to build statistics timeseries")
		}
		return harukiAPIHelper.SuccessResponse(c, "success", resp)
	}
}
