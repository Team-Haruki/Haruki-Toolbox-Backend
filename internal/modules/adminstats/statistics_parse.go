package adminstats

import (
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

func parseStatisticsTimeseriesBucket(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return timeseriesBucketHour, nil
	}
	switch trimmed {
	case timeseriesBucketHour, timeseriesBucketDay, timeseriesBucketWeek, timeseriesBucketMonth:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "bucket must be one of: hour, day, week, month")
	}
}

// parseStatisticsTimezone resolves the IANA timezone used to align timeseries
// buckets to local calendar boundaries. An empty value defaults to UTC, keeping
// the previous behaviour for callers that do not provide a timezone.
func parseStatisticsTimezone(raw string) (string, *time.Location, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultStatisticsTimezone, time.UTC, nil
	}

	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return "", nil, fiber.NewError(fiber.StatusBadRequest, "invalid timezone")
	}
	return trimmed, loc, nil
}
