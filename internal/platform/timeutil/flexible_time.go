package timeutil

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

func ParseFlexibleTime(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	if unixVal, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		var t time.Time
		if len(trimmed) > 10 {
			t = time.UnixMilli(unixVal).UTC()
		} else {
			t = time.Unix(unixVal, 0).UTC()
		}
		return &t, nil
	}

	t, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid time format, use RFC3339 or unix timestamp")
	}
	t = t.UTC()
	return &t, nil
}

func ResolveTimeRange(
	fromRaw, toRaw string,
	now time.Time,
	defaultWindow, maxRange time.Duration,
) (time.Time, time.Time, error) {
	fromValue, err := ParseFlexibleTime(fromRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	toValue, err := ParseFlexibleTime(toRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	now = now.UTC()
	var from time.Time
	var to time.Time

	switch {
	case fromValue == nil && toValue == nil:
		to = now
		from = to.Add(-defaultWindow)
	case fromValue == nil && toValue != nil:
		to = *toValue
		from = to.Add(-defaultWindow)
	case fromValue != nil && toValue == nil:
		from = *fromValue
		to = now
	default:
		from = *fromValue
		to = *toValue
	}

	if !from.Before(to) {
		return time.Time{}, time.Time{}, fiber.NewError(fiber.StatusBadRequest, "from must be earlier than to")
	}
	if maxRange > 0 && to.Sub(from) > maxRange {
		return time.Time{}, time.Time{}, fiber.NewError(fiber.StatusBadRequest, "time range exceeds max allowed window")
	}

	return from, to, nil
}
