package timeutil

import (
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseFlexibleTime(t *testing.T) {
	t.Parallel()

	t.Run("empty returns nil", func(t *testing.T) {
		t.Parallel()
		got, err := ParseFlexibleTime("")
		if err != nil {
			t.Fatalf("ParseFlexibleTime returned error: %v", err)
		}
		if got != nil {
			t.Fatalf("ParseFlexibleTime should return nil for empty input, got %v", got)
		}
	})

	t.Run("unix seconds", func(t *testing.T) {
		t.Parallel()
		got, err := ParseFlexibleTime("1700000000")
		if err != nil {
			t.Fatalf("ParseFlexibleTime returned error: %v", err)
		}
		want := time.Unix(1700000000, 0).UTC()
		if got == nil || !got.Equal(want) {
			t.Fatalf("ParseFlexibleTime unix seconds = %v, want %v", got, want)
		}
	})

	t.Run("unix millis", func(t *testing.T) {
		t.Parallel()
		got, err := ParseFlexibleTime("1700000000123")
		if err != nil {
			t.Fatalf("ParseFlexibleTime returned error: %v", err)
		}
		want := time.UnixMilli(1700000000123).UTC()
		if got == nil || !got.Equal(want) {
			t.Fatalf("ParseFlexibleTime unix millis = %v, want %v", got, want)
		}
	})

	t.Run("rfc3339", func(t *testing.T) {
		t.Parallel()
		got, err := ParseFlexibleTime("2026-03-09T12:34:56+08:00")
		if err != nil {
			t.Fatalf("ParseFlexibleTime returned error: %v", err)
		}
		want := time.Date(2026, 3, 9, 4, 34, 56, 0, time.UTC)
		if got == nil || !got.Equal(want) {
			t.Fatalf("ParseFlexibleTime rfc3339 = %v, want %v", got, want)
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		t.Parallel()
		_, err := ParseFlexibleTime("not-a-time")
		if err == nil {
			t.Fatalf("ParseFlexibleTime should fail for invalid time")
		}
		fiberErr, ok := err.(*fiber.Error)
		if !ok {
			t.Fatalf("error type = %T, want *fiber.Error", err)
		}
		if fiberErr.Code != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", fiberErr.Code, fiber.StatusBadRequest)
		}
	})
}
