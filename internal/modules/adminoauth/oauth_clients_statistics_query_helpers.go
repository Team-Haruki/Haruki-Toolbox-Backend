package adminoauth

import (
	"context"
	"database/sql"
	"fmt"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	"time"
)

func queryAdminOAuthAuthorizationTrendCounts(ctx context.Context, db *postgresql.Client, clientDBID int, from, to time.Time, bucket string) (map[int64]int, int, error) {
	var rawErr error
	if sqlDB := db.SQLDB(); sqlDB != nil {
		counts, total, err := queryAdminOAuthTrendCountsRawSQL(
			ctx,
			sqlDB,
			oauthauthorization.Table,
			oauthauthorization.ClientColumn,
			oauthauthorization.FieldCreatedAt,
			bucket,
			clientDBID,
			from,
			to,
		)
		if err == nil {
			return counts, total, nil
		}
		rawErr = err
	}

	rows, err := db.OAuthAuthorization.Query().Where(
		oauthauthorization.HasClientWith(oauthclient.IDEQ(clientDBID)),
		oauthauthorization.CreatedAtGTE(from),
		oauthauthorization.CreatedAtLTE(to),
	).Select(oauthauthorization.FieldCreatedAt).All(ctx)
	if err != nil {
		if rawErr != nil {
			return nil, 0, rawErr
		}
		return nil, 0, err
	}
	times := make([]time.Time, 0, len(rows))
	for _, row := range rows {
		times = append(times, row.CreatedAt)
	}
	return aggregateTrendCountsFromTimes(times, from, to, bucket), len(times), nil
}

func queryAdminOAuthTokenTrendCounts(ctx context.Context, db *postgresql.Client, clientDBID int, from, to time.Time, bucket string) (map[int64]int, int, error) {
	var rawErr error
	if sqlDB := db.SQLDB(); sqlDB != nil {
		counts, total, err := queryAdminOAuthTrendCountsRawSQL(
			ctx,
			sqlDB,
			oauthtoken.Table,
			oauthtoken.ClientColumn,
			oauthtoken.FieldCreatedAt,
			bucket,
			clientDBID,
			from,
			to,
		)
		if err == nil {
			return counts, total, nil
		}
		rawErr = err
	}

	rows, err := db.OAuthToken.Query().Where(
		oauthtoken.HasClientWith(oauthclient.IDEQ(clientDBID)),
		oauthtoken.CreatedAtGTE(from),
		oauthtoken.CreatedAtLTE(to),
	).Select(oauthtoken.FieldCreatedAt).All(ctx)
	if err != nil {
		if rawErr != nil {
			return nil, 0, rawErr
		}
		return nil, 0, err
	}
	times := make([]time.Time, 0, len(rows))
	for _, row := range rows {
		times = append(times, row.CreatedAt)
	}
	return aggregateTrendCountsFromTimes(times, from, to, bucket), len(times), nil
}

func queryAdminOAuthTrendCountsRawSQL(
	ctx context.Context,
	sqlDB *sql.DB,
	table string,
	clientColumn string,
	createdAtColumn string,
	bucket string,
	clientDBID int,
	from time.Time,
	to time.Time,
) (map[int64]int, int, error) {
	bucketExpr, err := buildAdminOAuthBucketExpressionSQL(bucket, createdAtColumn)
	if err != nil {
		return nil, 0, err
	}
	query := fmt.Sprintf(
		"SELECT EXTRACT(EPOCH FROM %s)::bigint AS bucket_unix, COUNT(*)::bigint AS count FROM %s WHERE %s = $1 AND %s >= $2 AND %s <= $3 GROUP BY bucket_unix",
		bucketExpr,
		table,
		clientColumn,
		createdAtColumn,
		createdAtColumn,
	)
	rows, err := sqlDB.QueryContext(ctx, query, clientDBID, from.UTC(), to.UTC())
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

	counts := make(map[int64]int)
	total := 0
	for rows.Next() {
		var bucketUnix int64
		var count int64
		if err := rows.Scan(&bucketUnix, &count); err != nil {
			return nil, 0, err
		}
		counts[bucketUnix] = int(count)
		total += int(count)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return counts, total, nil
}

func buildAdminOAuthBucketExpressionSQL(bucket, createdAtColumn string) (string, error) {
	switch bucket {
	case adminOAuthClientTrendBucketHour:
		return fmt.Sprintf("date_trunc('hour', %s AT TIME ZONE 'UTC')", createdAtColumn), nil
	case adminOAuthClientTrendBucketDay:
		return fmt.Sprintf("date_trunc('day', %s AT TIME ZONE 'UTC')", createdAtColumn), nil
	default:
		return "", fmt.Errorf("invalid bucket")
	}
}

func aggregateTrendCountsFromTimes(times []time.Time, from, to time.Time, bucket string) map[int64]int {
	from = from.UTC()
	to = to.UTC()
	counts := make(map[int64]int)
	for _, eventTime := range times {
		eventTime = eventTime.UTC()
		if eventTime.Before(from) || eventTime.After(to) {
			continue
		}
		bucketStart := truncateTimeByBucket(eventTime, bucket)
		counts[bucketStart.Unix()]++
	}
	return counts
}
