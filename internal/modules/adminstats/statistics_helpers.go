package adminstats

import (
	"context"
	stdsql "database/sql"
	"fmt"
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

func parseStatisticsWindowHours(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultStatisticsWindowHours, nil
	}

	hours64, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, fiber.NewError(fiber.StatusBadRequest, "hours must be an integer")
	}
	hours := int(hours64)
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
		out = append(out, categoryCount(row))
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
		out = append(out, methodDataTypeCount(row))
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

func buildStatisticsBucketExpressionSQL(bucket, columnName string) (string, error) {
	switch bucket {
	case timeseriesBucketHour:
		return fmt.Sprintf("date_trunc('hour', %s AT TIME ZONE 'UTC')", columnName), nil
	case timeseriesBucketDay:
		return fmt.Sprintf("date_trunc('day', %s AT TIME ZONE 'UTC')", columnName), nil
	default:
		return "", fmt.Errorf("invalid bucket")
	}
}

func queryRegistrationCountsRawSQL(queryCtx context.Context, sqlDB *stdsql.DB, from, to time.Time, bucket string) (map[int64]int, error) {
	bucketExpr, err := buildStatisticsBucketExpressionSQL(bucket, userSchema.FieldCreatedAt)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		"SELECT EXTRACT(EPOCH FROM %s)::bigint AS bucket_unix, COUNT(*)::bigint AS count FROM users WHERE %s IS NOT NULL AND %s >= $1 AND %s <= $2 GROUP BY bucket_unix",
		bucketExpr,
		userSchema.FieldCreatedAt,
		userSchema.FieldCreatedAt,
		userSchema.FieldCreatedAt,
	)
	rows, err := sqlDB.QueryContext(queryCtx, query, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	counts := make(map[int64]int)
	for rows.Next() {
		var bucketUnix int64
		var count int64
		if err := rows.Scan(&bucketUnix, &count); err != nil {
			return nil, err
		}
		counts[bucketUnix] = int(count)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

type statisticsUploadBucketCount struct {
	Total   int
	Success int
	Failure int
}

func queryUploadCountsRawSQL(queryCtx context.Context, sqlDB *stdsql.DB, from, to time.Time, bucket string) (map[int64]statisticsUploadBucketCount, error) {
	bucketExpr, err := buildStatisticsBucketExpressionSQL(bucket, uploadlog.FieldUploadTime)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(
		"SELECT EXTRACT(EPOCH FROM %s)::bigint AS bucket_unix, COUNT(*)::bigint AS total, SUM(CASE WHEN %s THEN 1 ELSE 0 END)::bigint AS success FROM upload_logs WHERE %s >= $1 AND %s <= $2 GROUP BY bucket_unix",
		bucketExpr,
		uploadlog.FieldSuccess,
		uploadlog.FieldUploadTime,
		uploadlog.FieldUploadTime,
	)
	rows, err := sqlDB.QueryContext(queryCtx, query, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	counts := make(map[int64]statisticsUploadBucketCount)
	for rows.Next() {
		var bucketUnix int64
		var total int64
		var success int64
		if err := rows.Scan(&bucketUnix, &total, &success); err != nil {
			return nil, err
		}
		failure := int(total - success)
		if failure < 0 {
			failure = 0
		}
		counts[bucketUnix] = statisticsUploadBucketCount{
			Total:   int(total),
			Success: int(success),
			Failure: failure,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

func queryRegistrationCountsFallback(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket string) (map[int64]int, error) {
	rows, err := apiHelper.DBManager.DB.User.Query().
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
	counts := make(map[int64]int, len(rows))
	for _, row := range rows {
		if row == nil || row.CreatedAt == nil {
			continue
		}
		key := truncateStatisticsTimeByBucket(row.CreatedAt.UTC(), bucket).Unix()
		counts[key]++
	}
	return counts, nil
}

func queryUploadCountsFallback(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket string) (map[int64]statisticsUploadBucketCount, error) {
	rows, err := apiHelper.DBManager.DB.UploadLog.Query().
		Where(
			uploadlog.UploadTimeGTE(from),
			uploadlog.UploadTimeLTE(to),
		).
		Select(uploadlog.FieldUploadTime, uploadlog.FieldSuccess).
		All(ctx.Context())
	if err != nil {
		return nil, err
	}

	counts := make(map[int64]statisticsUploadBucketCount)
	for _, row := range rows {
		key := truncateStatisticsTimeByBucket(row.UploadTime, bucket).Unix()
		current := counts[key]
		current.Total++
		if row.Success {
			current.Success++
		} else {
			current.Failure++
		}
		counts[key] = current
	}
	return counts, nil
}

func buildStatisticsTimeseries(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket string) (*statisticsTimeseriesResponse, error) {
	points := initializeTimeseriesPoints(from, to, bucket)

	db := apiHelper.DBManager.DB
	var (
		registrationCounts map[int64]int
		uploadCounts       map[int64]statisticsUploadBucketCount
		err                error
	)
	if sqlDB := db.SQLDB(); sqlDB != nil {
		registrationCounts, err = queryRegistrationCountsRawSQL(ctx.Context(), sqlDB, from, to, bucket)
		if err != nil {
			return nil, err
		}
		uploadCounts, err = queryUploadCountsRawSQL(ctx.Context(), sqlDB, from, to, bucket)
		if err != nil {
			return nil, err
		}
	} else {
		registrationCounts, err = queryRegistrationCountsFallback(ctx, apiHelper, from, to, bucket)
		if err != nil {
			return nil, err
		}
		uploadCounts, err = queryUploadCountsFallback(ctx, apiHelper, from, to, bucket)
		if err != nil {
			return nil, err
		}
	}

	for i := range points {
		key := points[i].Time.Unix()
		if registrations, ok := registrationCounts[key]; ok {
			points[i].Registrations = registrations
		}
		if upload, ok := uploadCounts[key]; ok {
			points[i].Uploads = upload.Total
			points[i].UploadSuccesses = upload.Success
			points[i].UploadFailures = upload.Failure
		}
	}

	resp := &statisticsTimeseriesResponse{
		GeneratedAt: adminNowUTC(),
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

	now := adminNowUTC()
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
