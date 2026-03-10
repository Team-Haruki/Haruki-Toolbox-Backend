package useractivity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func TestParseOwnActivityLogQueryFilters(t *testing.T) {
	now := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)

	app := fiber.New()
	var parsed *ownActivityLogQueryFilters
	app.Get("/", func(c fiber.Ctx) error {
		parsed = nil
		filters, err := parseOwnActivityLogQueryFilters(c, now)
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
		if parsed.Page != defaultUserActivityLogPage {
			t.Fatalf("page = %d, want %d", parsed.Page, defaultUserActivityLogPage)
		}
		if parsed.PageSize != defaultUserActivityLogPageSize {
			t.Fatalf("pageSize = %d, want %d", parsed.PageSize, defaultUserActivityLogPageSize)
		}
		if parsed.Sort != defaultUserActivityLogSort {
			t.Fatalf("sort = %q, want %q", parsed.Sort, defaultUserActivityLogSort)
		}
		if !parsed.To.Equal(now) {
			t.Fatalf("to = %s, want %s", parsed.To, now)
		}
		if !parsed.From.Equal(now.Add(-defaultUserActivityLogWindowHours * time.Hour)) {
			t.Fatalf("from = %s, want %s", parsed.From, now.Add(-defaultUserActivityLogWindowHours*time.Hour))
		}
	})

	t.Run("valid custom filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-07T00:00:00Z&to=2026-03-08T00:00:00Z&action=user.oauth&result=success&page=2&page_size=20&sort=event_time_asc", nil)
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
		if parsed.Action != "user.oauth" {
			t.Fatalf("action = %q, want %q", parsed.Action, "user.oauth")
		}
		if parsed.Result != harukiAPIHelper.SystemLogResultSuccess {
			t.Fatalf("result = %q, want %q", parsed.Result, harukiAPIHelper.SystemLogResultSuccess)
		}
		if parsed.Page != 2 || parsed.PageSize != 20 {
			t.Fatalf("unexpected paging: page=%d pageSize=%d", parsed.Page, parsed.PageSize)
		}
		if parsed.Sort != userActivityLogSortEventTimeAsc {
			t.Fatalf("sort = %q, want %q", parsed.Sort, userActivityLogSortEventTimeAsc)
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

	t.Run("invalid page_size", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page_size=999", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid action filter length", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?action="+strings.Repeat("a", maxUserActivityLogActionFilterLength+1), nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid time range", func(t *testing.T) {
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

func TestSanitizeOwnActivityMetadataMap(t *testing.T) {
	original := map[string]any{
		"sessionToken": "abc",
		"passwordHash": "def",
		"normal":       "ok",
		"nested": map[string]any{
			"refresh_token": "refresh",
			"note":          "nested-ok",
		},
		"list": []any{
			map[string]any{
				"authorization": "Bearer xxx",
				"plain":         "v",
			},
		},
		"long": strings.Repeat("x", maxUserActivityMetadataStringLength+10),
	}

	got := sanitizeOwnActivityMetadataMap(original)
	if got["sessionToken"] != redactedMetadataValue {
		t.Fatalf("sessionToken should be redacted, got %#v", got["sessionToken"])
	}
	if got["passwordHash"] != redactedMetadataValue {
		t.Fatalf("passwordHash should be redacted, got %#v", got["passwordHash"])
	}

	nested, ok := got["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested should be map[string]any, got %#v", got["nested"])
	}
	if nested["refresh_token"] != redactedMetadataValue {
		t.Fatalf("refresh_token should be redacted, got %#v", nested["refresh_token"])
	}
	if nested["note"] != "nested-ok" {
		t.Fatalf("nested.note should stay unchanged, got %#v", nested["note"])
	}

	list, ok := got["list"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("list should contain one item, got %#v", got["list"])
	}
	listMap, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("list[0] should be map[string]any, got %#v", list[0])
	}
	if listMap["authorization"] != redactedMetadataValue {
		t.Fatalf("authorization should be redacted, got %#v", listMap["authorization"])
	}
	if listMap["plain"] != "v" {
		t.Fatalf("plain should stay unchanged, got %#v", listMap["plain"])
	}

	longValue, ok := got["long"].(string)
	if !ok {
		t.Fatalf("long should be string, got %#v", got["long"])
	}
	if !strings.HasSuffix(longValue, "...") {
		t.Fatalf("long should be truncated with suffix, got %q", longValue)
	}
}

func TestHandleListOwnActivityLogsRejectOtherUser(t *testing.T) {
	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	app.Get("/api/user/:toolbox_user_id/activity-logs", func(c fiber.Ctx) error {
		c.Locals("userID", "1001")
		c.Locals("userRole", "user")
		return handleListOwnActivityLogs(apiHelper)(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/2002/activity-logs", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload["message"] != "you can only access your own activity logs" {
		t.Fatalf("message = %#v, want own activity logs error", payload["message"])
	}
}

func TestHandleListOwnActivityLogsDatabaseUnavailable(t *testing.T) {
	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	app.Get("/api/user/:toolbox_user_id/activity-logs", func(c fiber.Ctx) error {
		c.Locals("userID", "1001")
		c.Locals("userRole", "user")
		return handleListOwnActivityLogs(apiHelper)(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/1001/activity-logs", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusInternalServerError)
	}
}
