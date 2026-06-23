package adminstats

const (
	defaultStatisticsWindowHours = 24
	maxStatisticsWindowHours     = 24 * 366 // up to ~1 year (leap-safe); coarse buckets keep point count sane
)

const (
	timeseriesBucketHour  = "hour"
	timeseriesBucketDay   = "day"
	timeseriesBucketWeek  = "week"
	timeseriesBucketMonth = "month"
)

const defaultStatisticsTimezone = "UTC"
