package timeutil

import (
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestResolveTimeRange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	defaultWindow := 24 * time.Hour
	maxRange := 30 * 24 * time.Hour

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		from, to, err := ResolveTimeRange("", "", now, defaultWindow, maxRange)
		if err != nil {
			t.Fatalf("ResolveTimeRange returned error: %v", err)
		}
		if !to.Equal(now) {
			t.Fatalf("to = %s, want %s", to, now)
		}
		wantFrom := now.Add(-defaultWindow)
		if !from.Equal(wantFrom) {
			t.Fatalf("from = %s, want %s", from, wantFrom)
		}
	})

	t.Run("from only", func(t *testing.T) {
		t.Parallel()
		from, to, err := ResolveTimeRange("2026-03-08T00:00:00Z", "", now, defaultWindow, maxRange)
		if err != nil {
			t.Fatalf("ResolveTimeRange returned error: %v", err)
		}
		if !from.Equal(time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("unexpected from: %s", from)
		}
		if !to.Equal(now) {
			t.Fatalf("to = %s, want %s", to, now)
		}
	})

	t.Run("to only", func(t *testing.T) {
		t.Parallel()
		from, to, err := ResolveTimeRange("", "2026-03-09T00:00:00Z", now, defaultWindow, maxRange)
		if err != nil {
			t.Fatalf("ResolveTimeRange returned error: %v", err)
		}
		wantTo := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
		if !to.Equal(wantTo) {
			t.Fatalf("to = %s, want %s", to, wantTo)
		}
		if !from.Equal(wantTo.Add(-defaultWindow)) {
			t.Fatalf("from = %s, want %s", from, wantTo.Add(-defaultWindow))
		}
	})

	t.Run("invalid order", func(t *testing.T) {
		t.Parallel()
		_, _, err := ResolveTimeRange("2026-03-09T02:00:00Z", "2026-03-09T01:00:00Z", now, defaultWindow, maxRange)
		if err == nil {
			t.Fatalf("ResolveTimeRange should fail for invalid order")
		}
		fiberErr, ok := err.(*fiber.Error)
		if !ok {
			t.Fatalf("error type = %T, want *fiber.Error", err)
		}
		if fiberErr.Code != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", fiberErr.Code, fiber.StatusBadRequest)
		}
	})

	t.Run("exceeds max range", func(t *testing.T) {
		t.Parallel()
		_, _, err := ResolveTimeRange("2026-01-01T00:00:00Z", "2026-03-09T00:00:00Z", now, defaultWindow, maxRange)
		if err == nil {
			t.Fatalf("ResolveTimeRange should fail when range exceeds max")
		}
		fiberErr, ok := err.(*fiber.Error)
		if !ok {
			t.Fatalf("error type = %T, want *fiber.Error", err)
		}
		if fiberErr.Code != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", fiberErr.Code, fiber.StatusBadRequest)
		}
	})

	t.Run("ignore max range when non-positive", func(t *testing.T) {
		t.Parallel()
		_, _, err := ResolveTimeRange("2026-01-01T00:00:00Z", "2026-03-09T00:00:00Z", now, defaultWindow, 0)
		if err != nil {
			t.Fatalf("ResolveTimeRange should not enforce max when maxRange<=0, err=%v", err)
		}
	})
}

func TestResolveUploadLogTimeRange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)

	from, to, err := ResolveUploadLogTimeRange("", "", now)
	if err != nil {
		t.Fatalf("ResolveUploadLogTimeRange returned error: %v", err)
	}
	if !to.Equal(now) {
		t.Fatalf("to = %s, want %s", to, now)
	}
	if !from.Equal(now.Add(-DefaultUploadLogWindow)) {
		t.Fatalf("from = %s, want %s", from, now.Add(-DefaultUploadLogWindow))
	}
}
