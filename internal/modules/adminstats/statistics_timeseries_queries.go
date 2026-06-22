package adminstats

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

type statisticsUploadBucketCount struct {
	Total   int
	Success int
	Failure int
}

// buildStatisticsBucketExpressionSQL builds a bucket-start expression aligned to
// the given timezone. The column (a timestamptz) is converted to local wall time,
// truncated to the bucket unit, then re-anchored to an absolute instant via the
// second AT TIME ZONE so EXTRACT(EPOCH ...) matches truncateStatisticsTimeByBucket.
// tzPlaceholder is a bound-parameter placeholder (e.g. "$3") to avoid injecting
// the timezone string into the query text.
func buildStatisticsBucketExpressionSQL(bucket, columnName, tzPlaceholder string) (string, error) {
	var unit string
	switch bucket {
	case timeseriesBucketHour:
		unit = "hour"
	case timeseriesBucketDay:
		unit = "day"
	case timeseriesBucketWeek:
		unit = "week"
	case timeseriesBucketMonth:
		unit = "month"
	default:
		return "", fmt.Errorf("invalid bucket")
	}
	return fmt.Sprintf(
		"date_trunc('%s', %s AT TIME ZONE %s) AT TIME ZONE %s",
		unit, columnName, tzPlaceholder, tzPlaceholder,
	), nil
}

func queryRegistrationCountsRawSQL(queryCtx context.Context, sqlDB *stdsql.DB, from, to time.Time, bucket, tz string) (map[int64]int, error) {
	bucketExpr, err := buildStatisticsBucketExpressionSQL(bucket, userSchema.FieldCreatedAt, "$3")
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
	rows, err := sqlDB.QueryContext(queryCtx, query, from.UTC(), to.UTC(), tz)
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

func queryUploadCountsRawSQL(queryCtx context.Context, sqlDB *stdsql.DB, from, to time.Time, bucket, tz string) (map[int64]statisticsUploadBucketCount, error) {
	bucketExpr, err := buildStatisticsBucketExpressionSQL(bucket, uploadlog.FieldUploadTime, "$3")
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
	rows, err := sqlDB.QueryContext(queryCtx, query, from.UTC(), to.UTC(), tz)
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

func queryRegistrationCountsFallback(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket string, loc *time.Location) (map[int64]int, error) {
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
		key := truncateStatisticsTimeByBucket(*row.CreatedAt, bucket, loc).Unix()
		counts[key]++
	}
	return counts, nil
}

func queryUploadCountsFallback(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, from, to time.Time, bucket string, loc *time.Location) (map[int64]statisticsUploadBucketCount, error) {
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
		key := truncateStatisticsTimeByBucket(row.UploadTime, bucket, loc).Unix()
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
