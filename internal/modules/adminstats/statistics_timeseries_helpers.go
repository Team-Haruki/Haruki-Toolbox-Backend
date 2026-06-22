package adminstats

import (
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
)

// truncateStatisticsTimeByBucket aligns t to the start of its bucket in loc.
// Buckets are anchored to local calendar boundaries (weeks start on Monday, to
// match Postgres date_trunc('week', ...)), so the resulting instant matches the
// bucket_unix produced by the SQL query for the same timezone.
func truncateStatisticsTimeByBucket(t time.Time, bucket string, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	t = t.In(loc)
	switch bucket {
	case timeseriesBucketDay:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	case timeseriesBucketWeek:
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		// Shift back to Monday: Go's Weekday() has Sunday=0, so map Monday=0.
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset)
	case timeseriesBucketMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
	}
}

func nextStatisticsBucket(t time.Time, bucket string) time.Time {
	switch bucket {
	case timeseriesBucketDay:
		return t.AddDate(0, 0, 1)
	case timeseriesBucketWeek:
		return t.AddDate(0, 0, 7)
	case timeseriesBucketMonth:
		return t.AddDate(0, 1, 0)
	default:
		return t.Add(time.Hour)
	}
}

func initializeTimeseriesPoints(from, to time.Time, bucket string, loc *time.Location) []statisticsTimeseriesPoint {
	start := truncateStatisticsTimeByBucket(from, bucket, loc)
	end := truncateStatisticsTimeByBucket(to, bucket, loc)

	points := make([]statisticsTimeseriesPoint, 0)
	for ts := start; !ts.After(end); ts = nextStatisticsBucket(ts, bucket) {
		points = append(points, statisticsTimeseriesPoint{Time: ts})
	}
	return points
}

func accumulateRegistrationTimeseriesFromUsers(rows []*postgresql.User, pointByTime map[time.Time]*statisticsTimeseriesPoint, bucket string, loc *time.Location) {
	for _, row := range rows {
		if row == nil || row.CreatedAt == nil {
			continue
		}
		key := truncateStatisticsTimeByBucket(*row.CreatedAt, bucket, loc)
		if p, ok := pointByTime[key]; ok {
			p.Registrations++
		}
	}
}
