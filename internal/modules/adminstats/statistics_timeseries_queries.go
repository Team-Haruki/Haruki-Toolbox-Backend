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

// The queries are deliberately complete constants rather than dynamically
// formatted SQL. The selected bucket is still allow-listed, while the timezone
// remains a bound parameter. A timestamptz is converted to local wall time,
// truncated, then re-anchored so its epoch matches truncateStatisticsTimeByBucket.
const (
	registrationCountsHourSQL  = "SELECT EXTRACT(EPOCH FROM date_trunc('hour', created_at AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS count FROM users WHERE created_at IS NOT NULL AND created_at >= $1 AND created_at <= $2 GROUP BY bucket_unix"
	registrationCountsDaySQL   = "SELECT EXTRACT(EPOCH FROM date_trunc('day', created_at AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS count FROM users WHERE created_at IS NOT NULL AND created_at >= $1 AND created_at <= $2 GROUP BY bucket_unix"
	registrationCountsWeekSQL  = "SELECT EXTRACT(EPOCH FROM date_trunc('week', created_at AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS count FROM users WHERE created_at IS NOT NULL AND created_at >= $1 AND created_at <= $2 GROUP BY bucket_unix"
	registrationCountsMonthSQL = "SELECT EXTRACT(EPOCH FROM date_trunc('month', created_at AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS count FROM users WHERE created_at IS NOT NULL AND created_at >= $1 AND created_at <= $2 GROUP BY bucket_unix"

	uploadCountsHourSQL  = "SELECT EXTRACT(EPOCH FROM date_trunc('hour', upload_time AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS total, SUM(CASE WHEN success THEN 1 ELSE 0 END)::bigint AS success FROM upload_logs WHERE upload_time >= $1 AND upload_time <= $2 GROUP BY bucket_unix"
	uploadCountsDaySQL   = "SELECT EXTRACT(EPOCH FROM date_trunc('day', upload_time AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS total, SUM(CASE WHEN success THEN 1 ELSE 0 END)::bigint AS success FROM upload_logs WHERE upload_time >= $1 AND upload_time <= $2 GROUP BY bucket_unix"
	uploadCountsWeekSQL  = "SELECT EXTRACT(EPOCH FROM date_trunc('week', upload_time AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS total, SUM(CASE WHEN success THEN 1 ELSE 0 END)::bigint AS success FROM upload_logs WHERE upload_time >= $1 AND upload_time <= $2 GROUP BY bucket_unix"
	uploadCountsMonthSQL = "SELECT EXTRACT(EPOCH FROM date_trunc('month', upload_time AT TIME ZONE $3) AT TIME ZONE $3)::bigint AS bucket_unix, COUNT(*)::bigint AS total, SUM(CASE WHEN success THEN 1 ELSE 0 END)::bigint AS success FROM upload_logs WHERE upload_time >= $1 AND upload_time <= $2 GROUP BY bucket_unix"
)

func registrationCountsSQL(bucket string) (string, error) {
	switch bucket {
	case timeseriesBucketHour:
		return registrationCountsHourSQL, nil
	case timeseriesBucketDay:
		return registrationCountsDaySQL, nil
	case timeseriesBucketWeek:
		return registrationCountsWeekSQL, nil
	case timeseriesBucketMonth:
		return registrationCountsMonthSQL, nil
	default:
		return "", fmt.Errorf("invalid statistics bucket %q", bucket)
	}
}

func uploadCountsSQL(bucket string) (string, error) {
	switch bucket {
	case timeseriesBucketHour:
		return uploadCountsHourSQL, nil
	case timeseriesBucketDay:
		return uploadCountsDaySQL, nil
	case timeseriesBucketWeek:
		return uploadCountsWeekSQL, nil
	case timeseriesBucketMonth:
		return uploadCountsMonthSQL, nil
	default:
		return "", fmt.Errorf("invalid statistics bucket %q", bucket)
	}
}

func queryRegistrationCountsRawSQL(queryCtx context.Context, sqlDB *stdsql.DB, from, to time.Time, bucket, tz string) (map[int64]int, error) {
	query, err := registrationCountsSQL(bucket)
	if err != nil {
		return nil, err
	}
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
	query, err := uploadCountsSQL(bucket)
	if err != nil {
		return nil, err
	}
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
