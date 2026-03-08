package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminOAuthClientAuditFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		filters, err := parseAdminOAuthClientAuditFilters(c, time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		return c.SendString(strings.Join([]string{
			strings.Join(filters.ActorTypes, "|"),
			filters.ActorUserID,
			filters.Action,
			filters.Result,
			strconv.Itoa(filters.Page),
			strconv.Itoa(filters.PageSize),
			filters.Sort,
		}, ","))
	})

	t.Run("defaults", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != ",,,,1,50,event_time_desc" {
			t.Fatalf("response body = %q, want %q", string(body), ",,,,1,50,event_time_desc")
		}
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-08T00:00:00Z&to=2026-03-08T12:00:00Z&actor_type=admin,user&actor_user_id=1001&action=admin.oauth_client.revoke&result=failure&page=2&page_size=20&sort=id_asc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "admin|user,1001,admin.oauth_client.revoke,failure,2,20,id_asc" {
			t.Fatalf("response body = %q, want %q", string(body), "admin|user,1001,admin.oauth_client.revoke,failure,2,20,id_asc")
		}
	})

	t.Run("invalid actor type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?actor_type=owner", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid result", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?result=ok", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid page size", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page_size=999", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid sort", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?sort=created_desc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}
