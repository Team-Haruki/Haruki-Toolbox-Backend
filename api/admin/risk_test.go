package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseRiskEventFilters(t *testing.T) {
	now := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseRiskEventFilters(c, now)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?status=open&severity=high&actor_user_id=1001&target_user_id=2002&action=login&page=2&page_size=20&sort=event_time_asc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?status=done", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}
