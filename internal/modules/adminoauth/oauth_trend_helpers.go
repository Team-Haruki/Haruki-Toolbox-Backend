package adminoauth

import (
	"fmt"
	"regexp"
	"time"
)

// validSQLIdentifier matches a simple SQL identifier, optionally qualified with a single dot,
// e.g. "created_at" or "table.created_at". This prevents injection via arbitrary SQL syntax.
var validSQLIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?$`)

func buildAdminOAuthBucketExpressionSQL(bucket, createdAtColumn string) (string, error) {
	if !validSQLIdentifier.MatchString(createdAtColumn) {
		return "", fmt.Errorf("invalid createdAtColumn")
	}

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
