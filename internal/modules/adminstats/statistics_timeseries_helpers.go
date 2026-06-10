package adminstats

import (
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
)

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
