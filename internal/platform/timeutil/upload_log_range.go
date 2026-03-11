package timeutil

import "time"

const (
	DefaultUploadLogWindow = 24 * time.Hour
	MaxUploadLogTimeRange  = 30 * 24 * time.Hour
)

func ResolveUploadLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	return ResolveTimeRange(fromRaw, toRaw, now, DefaultUploadLogWindow, MaxUploadLogTimeRange)
}
