package adminusers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminUserActivityFilters(t *testing.T) {
	now := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)

	app := fiber.New()
	var parsed *adminUserActivityFilters
	app.Get("/", func(c fiber.Ctx) error {
		parsed = nil
		filters, err := parseAdminUserActivityFilters(c, now)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		parsed = filters
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("defaults", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
		if parsed == nil {
			t.Fatalf("parsed filters is nil")
		}
		if parsed.SystemLogLimit != defaultAdminUserActivitySystemLogLimit {
			t.Fatalf("systemLogLimit = %d, want %d", parsed.SystemLogLimit, defaultAdminUserActivitySystemLogLimit)
		}
		if parsed.UploadLogLimit != defaultAdminUserActivityUploadLogLimit {
			t.Fatalf("uploadLogLimit = %d, want %d", parsed.UploadLogLimit, defaultAdminUserActivityUploadLogLimit)
		}
		if !parsed.To.Equal(now) {
			t.Fatalf("to = %s, want %s", parsed.To, now)
		}
		if !parsed.From.Equal(now.Add(-24 * time.Hour)) {
			t.Fatalf("from = %s, want %s", parsed.From, now.Add(-24*time.Hour))
		}
	})

	t.Run("valid custom filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-07T00:00:00Z&to=2026-03-08T00:00:00Z&system_log_limit=80&upload_log_limit=120", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
		if parsed == nil {
			t.Fatalf("parsed filters is nil")
		}
		if parsed.SystemLogLimit != 80 {
			t.Fatalf("systemLogLimit = %d, want 80", parsed.SystemLogLimit)
		}
		if parsed.UploadLogLimit != 120 {
			t.Fatalf("uploadLogLimit = %d, want 120", parsed.UploadLogLimit)
		}
	})

	t.Run("reject invalid system_log_limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?system_log_limit=999", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid upload_log_limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?upload_log_limit=0", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid time range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-08T12:00:00Z&to=2026-03-08T11:00:00Z", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}
