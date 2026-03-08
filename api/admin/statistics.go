package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/uploadlog"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	defaultStatisticsWindowHours = 24
	maxStatisticsWindowHours     = 24 * 30
)

type categoryCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type methodDataTypeCount struct {
	UploadMethod string `json:"uploadMethod"`
	DataType     string `json:"dataType"`
	Count        int    `json:"count"`
}

type dashboardUserStats struct {
	Total      int `json:"total"`
	Banned     int `json:"banned"`
	Admin      int `json:"admin"`
	SuperAdmin int `json:"superAdmin"`
}

type dashboardGameBindingStats struct {
	Total    int             `json:"total"`
	Verified int             `json:"verified"`
	ByServer []categoryCount `json:"byServer"`
}

type dashboardUploadStats struct {
	WindowHours         int                   `json:"windowHours"`
	WindowStart         time.Time             `json:"windowStart"`
	WindowEnd           time.Time             `json:"windowEnd"`
	TotalAllTime        int                   `json:"totalAllTime"`
	Total               int                   `json:"total"`
	Success             int                   `json:"success"`
	Failed              int                   `json:"failed"`
	ByMethod            []categoryCount       `json:"byMethod"`
	ByDataType          []categoryCount       `json:"byDataType"`
	ByMethodAndDataType []methodDataTypeCount `json:"byMethodAndDataType"`
}

type dashboardStatisticsResponse struct {
	GeneratedAt time.Time                 `json:"generatedAt"`
	Users       dashboardUserStats        `json:"users"`
	Bindings    dashboardGameBindingStats `json:"bindings"`
	Uploads     dashboardUploadStats      `json:"uploads"`
}

type statisticsTimeseriesPoint struct {
	Time            time.Time `json:"time"`
	Registrations   int       `json:"registrations"`
	Uploads         int       `json:"uploads"`
	UploadSuccesses int       `json:"uploadSuccesses"`
	UploadFailures  int       `json:"uploadFailures"`
}

type statisticsTimeseriesResponse struct {
	GeneratedAt time.Time                   `json:"generatedAt"`
	From        time.Time                   `json:"from"`
	To          time.Time                   `json:"to"`
	Bucket      string                      `json:"bucket"`
	Points      []statisticsTimeseriesPoint `json:"points"`
}

type groupedFieldCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type groupedMethodDataTypeCount struct {
	UploadMethod string `json:"upload_method"`
	DataType     string `json:"data_type"`
	Count        int    `json:"count"`
}

const (
	timeseriesBucketHour = "hour"
	timeseriesBucketDay  = "day"
)

func parseStatisticsWindowHours(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultStatisticsWindowHours, nil
	}

	hours, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fiber.NewError(fiber.StatusBadRequest, "hours must be an integer")
	}
	if hours <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "hours must be greater than 0")
	}
	if hours > maxStatisticsWindowHours {
		return 0, fiber.NewError(fiber.StatusBadRequest, "hours exceeds max range")
	}

	return hours, nil
}

func normalizeCategoryCounts(rows []groupedFieldCount) []categoryCount {
	out := make([]categoryCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryCount{Key: row.Key, Count: row.Count})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Key < out[j].Key
		}
		return out[i].Count > out[j].Count
	})

	return out
}

func normalizeMethodDataTypeCounts(rows []groupedMethodDataTypeCount) []methodDataTypeCount {
	out := make([]methodDataTypeCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, methodDataTypeCount{
			UploadMethod: row.UploadMethod,
			DataType:     row.DataType,
			Count:        row.Count,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			if out[i].UploadMethod == out[j].UploadMethod {
				return out[i].DataType < out[j].DataType
			}
			return out[i].UploadMethod < out[j].UploadMethod
		}
		return out[i].Count > out[j].Count
	})

	return out
}

func parseStatisticsTimeseriesBucket(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return timeseriesBucketHour, nil
	}
	switch trimmed {
	case timeseriesBucketHour, timeseriesBucketDay:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "bucket must be one of: hour, day")
	}
}

func truncateStatisticsTimeByBucket(t time.Time, bucket string) time.Time {
	t = t.UTC()
	switch bucket {
	case timeseriesBucketDay:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	default:
		return t.Truncate(time.Hour)
	}
}

func bucketStepDuration(bucket string) time.Duration {
	switch bucket {
	case timeseriesBucketDay:
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

func initializeTimeseriesPoints(from, to time.Time, bucket string) []statisticsTimeseriesPoint {
	step := bucketStepDuration(bucket)
	start := truncateStatisticsTimeByBucket(from, bucket)
	end := truncateStatisticsTimeByBucket(to, bucket)

	points := make([]statisticsTimeseriesPoint, 0, int(end.Sub(start)/step)+1)
	for ts := start; !ts.After(end); ts = ts.Add(step) {
		points = append(points, statisticsTimeseriesPoint{Time: ts})
	}
	return points
}

func accumulateRegistrationTimeseriesFromUsers(rows []*postgresql.User, pointByTime map[time.Time]*statisticsTimeseriesPoint, bucket string) {
	for _, row := range rows {
		if row == nil || row.CreatedAt == nil {
			continue
		}
		key := truncateStatisticsTimeByBucket(row.CreatedAt.UTC(), bucket)
		if p, ok := pointByTime[key]; ok {
			p.Registrations++
		}
	}
}

func buildStatisticsTimeseries(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket string) (*statisticsTimeseriesResponse, error) {
	points := initializeTimeseriesPoints(from, to, bucket)
	pointByTime := make(map[time.Time]*statisticsTimeseriesPoint, len(points))
	for i := range points {
		p := &points[i]
		pointByTime[p.Time] = p
	}

	userRows, err := apiHelper.DBManager.DB.User.Query().
		Where(
			userSchema.CreatedAtNotNil(),
			userSchema.CreatedAtGTE(from),
			userSchema.CreatedAtLTE(to),
		).
		Select(userSchema.FieldCreatedAt).
		All(ctx.Context())
	if err != nil {
		return nil, err
	}
	accumulateRegistrationTimeseriesFromUsers(userRows, pointByTime, bucket)

	uploadRows, err := apiHelper.DBManager.DB.UploadLog.Query().
		Where(
			uploadlog.UploadTimeGTE(from),
			uploadlog.UploadTimeLTE(to),
		).
		Select(uploadlog.FieldUploadTime, uploadlog.FieldSuccess).
		All(ctx.Context())
	if err != nil {
		return nil, err
	}
	for _, row := range uploadRows {
		key := truncateStatisticsTimeByBucket(row.UploadTime, bucket)
		if p, ok := pointByTime[key]; ok {
			p.Uploads++
			if row.Success {
				p.UploadSuccesses++
			} else {
				p.UploadFailures++
			}
		}
	}

	resp := &statisticsTimeseriesResponse{
		GeneratedAt: time.Now().UTC(),
		From:        from.UTC(),
		To:          to.UTC(),
		Bucket:      bucket,
		Points:      points,
	}
	return resp, nil
}

func buildDashboardStatistics(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, windowHours int) (*dashboardStatisticsResponse, error) {
	db := apiHelper.DBManager.DB
	dbCtx := ctx.Context()

	now := time.Now().UTC()
	windowStart := now.Add(-time.Duration(windowHours) * time.Hour)

	userTotal, err := db.User.Query().Count(dbCtx)
	if err != nil {
		return nil, err
	}
	userBanned, err := db.User.Query().Where(userSchema.BannedEQ(true)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	adminCount, err := db.User.Query().Where(userSchema.RoleEQ(userSchema.RoleAdmin)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	superAdminCount, err := db.User.Query().Where(userSchema.RoleEQ(userSchema.RoleSuperAdmin)).Count(dbCtx)
	if err != nil {
		return nil, err
	}

	bindingTotal, err := db.GameAccountBinding.Query().Count(dbCtx)
	if err != nil {
		return nil, err
	}
	bindingVerified, err := db.GameAccountBinding.Query().Where(gameaccountbinding.VerifiedEQ(true)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	var bindingByServerRows []struct {
		Key   string `json:"server"`
		Count int    `json:"count"`
	}
	if err := db.GameAccountBinding.Query().
		GroupBy(gameaccountbinding.FieldServer).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &bindingByServerRows); err != nil {
		return nil, err
	}
	bindingByServer := make([]groupedFieldCount, 0, len(bindingByServerRows))
	for _, row := range bindingByServerRows {
		bindingByServer = append(bindingByServer, groupedFieldCount{Key: row.Key, Count: row.Count})
	}

	uploadTotalAllTime, err := db.UploadLog.Query().Count(dbCtx)
	if err != nil {
		return nil, err
	}
	uploadTotalWindow, err := db.UploadLog.Query().Where(uploadlog.UploadTimeGTE(windowStart)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	uploadSuccessWindow, err := db.UploadLog.Query().Where(uploadlog.UploadTimeGTE(windowStart), uploadlog.SuccessEQ(true)).Count(dbCtx)
	if err != nil {
		return nil, err
	}
	uploadFailedWindow := uploadTotalWindow - uploadSuccessWindow
	if uploadFailedWindow < 0 {
		uploadFailedWindow = 0
	}

	var uploadByMethodRows []struct {
		Key   string `json:"upload_method"`
		Count int    `json:"count"`
	}
	if err := db.UploadLog.Query().
		Where(uploadlog.UploadTimeGTE(windowStart)).
		GroupBy(uploadlog.FieldUploadMethod).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &uploadByMethodRows); err != nil {
		return nil, err
	}
	uploadByMethod := make([]groupedFieldCount, 0, len(uploadByMethodRows))
	for _, row := range uploadByMethodRows {
		uploadByMethod = append(uploadByMethod, groupedFieldCount{Key: row.Key, Count: row.Count})
	}

	var uploadByDataTypeRows []struct {
		Key   string `json:"data_type"`
		Count int    `json:"count"`
	}
	if err := db.UploadLog.Query().
		Where(uploadlog.UploadTimeGTE(windowStart)).
		GroupBy(uploadlog.FieldDataType).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &uploadByDataTypeRows); err != nil {
		return nil, err
	}
	uploadByDataType := make([]groupedFieldCount, 0, len(uploadByDataTypeRows))
	for _, row := range uploadByDataTypeRows {
		uploadByDataType = append(uploadByDataType, groupedFieldCount{Key: row.Key, Count: row.Count})
	}

	var methodDataTypeRows []groupedMethodDataTypeCount
	if err := db.UploadLog.Query().
		Where(uploadlog.UploadTimeGTE(windowStart)).
		GroupBy(uploadlog.FieldUploadMethod, uploadlog.FieldDataType).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(dbCtx, &methodDataTypeRows); err != nil {
		return nil, err
	}

	resp := &dashboardStatisticsResponse{
		GeneratedAt: now,
		Users: dashboardUserStats{
			Total:      userTotal,
			Banned:     userBanned,
			Admin:      adminCount,
			SuperAdmin: superAdminCount,
		},
		Bindings: dashboardGameBindingStats{
			Total:    bindingTotal,
			Verified: bindingVerified,
			ByServer: normalizeCategoryCounts(bindingByServer),
		},
		Uploads: dashboardUploadStats{
			WindowHours:         windowHours,
			WindowStart:         windowStart,
			WindowEnd:           now,
			TotalAllTime:        uploadTotalAllTime,
			Total:               uploadTotalWindow,
			Success:             uploadSuccessWindow,
			Failed:              uploadFailedWindow,
			ByMethod:            normalizeCategoryCounts(uploadByMethod),
			ByDataType:          normalizeCategoryCounts(uploadByDataType),
			ByMethodAndDataType: normalizeMethodDataTypeCounts(methodDataTypeRows),
		},
	}

	return resp, nil
}

func handleGetDashboardStatistics(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		windowHours, err := parseStatisticsWindowHours(c.Query("hours"))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid hours")
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
		from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), time.Now())
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid time range")
		}

		bucket, err := parseStatisticsTimeseriesBucket(c.Query("bucket"))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid bucket")
		}

		resp, err := buildStatisticsTimeseries(c, apiHelper, from, to, bucket)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to build statistics timeseries")
		}
		return harukiAPIHelper.SuccessResponse(c, "success", resp)
	}
}

func registerAdminStatisticsRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	statistics := adminGroup.Group("/statistics", RequireAdmin(apiHelper))
	statistics.Get("/dashboard", handleGetDashboardStatistics(apiHelper))
	statistics.Get("/upload-logs", handleQueryUploadLogs(apiHelper))
	statistics.Get("/timeseries", handleGetStatisticsTimeseries(apiHelper))
}
