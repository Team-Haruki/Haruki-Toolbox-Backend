//go:build legacy_oauth

package adminoauth

import (
	"fmt"
	"haruki-suite/utils/database/postgresql"
	"time"
)

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

func applyLatestExpiresAtByUser(statsByUserID map[string]adminOAuthTokenStats, rows []*postgresql.OAuthToken) {
	seenLatestByUserID := make(map[string]struct{}, len(statsByUserID))
	for _, row := range rows {
		if row == nil || row.Edges.User == nil {
			continue
		}
		userID := row.Edges.User.ID
		if _, seen := seenLatestByUserID[userID]; seen {
			continue
		}
		seenLatestByUserID[userID] = struct{}{}
		if row.ExpiresAt == nil {
			continue
		}
		stats := statsByUserID[userID]
		expiresAt := row.ExpiresAt.UTC()
		stats.LatestExpiresAt = &expiresAt
		statsByUserID[userID] = stats
	}
}

func finalizeAdminOAuthTokenStatsByUser(statsByUserID map[string]adminOAuthTokenStats) {
	for userID, stats := range statsByUserID {
		if stats.Active > stats.Total {
			stats.Active = stats.Total
		}
		stats.Revoked = stats.Total - stats.Active
		statsByUserID[userID] = stats
	}
}
