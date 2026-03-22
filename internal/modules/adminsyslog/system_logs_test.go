package adminsyslog

import (
	"haruki-suite/utils/database/postgresql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseSystemLogActorTypesFilter(t *testing.T) {
	values, err := parseSystemLogActorTypesFilter("admin,user")
	if err != nil {
		t.Fatalf("parseSystemLogActorTypesFilter returned error: %v", err)
	}
	if len(values) != 2 || values[0] != "admin" || values[1] != "user" {
		t.Fatalf("unexpected actor types: %#v", values)
	}

	if _, err := parseSystemLogActorTypesFilter("admin,owner"); err == nil {
		t.Fatalf("expected invalid actor_type filter to fail")
	}
}

func TestParseSystemLogResultFilter(t *testing.T) {
	result, err := parseSystemLogResultFilter("success")
	if err != nil {
		t.Fatalf("parseSystemLogResultFilter returned error: %v", err)
	}
	if result != "success" {
		t.Fatalf("result = %q, want success", result)
	}

	if _, err := parseSystemLogResultFilter("unknown"); err == nil {
		t.Fatalf("expected invalid result filter to fail")
	}
}

func TestParseSystemLogSort(t *testing.T) {
	sortValue, err := parseSystemLogSort("")
	if err != nil {
		t.Fatalf("parseSystemLogSort returned error: %v", err)
	}
	if sortValue != defaultSystemLogSort {
		t.Fatalf("sortValue = %q, want %q", sortValue, defaultSystemLogSort)
	}

	if _, err := parseSystemLogSort("invalid"); err == nil {
		t.Fatalf("expected invalid sort to fail")
	}
}

func TestParseSystemLogQueryFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseSystemLogQueryFilters(c, time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-08T00:00:00Z&to=2026-03-08T12:00:00Z&actor_type=admin,user&actor_user_id=1001&target_type=user&target_id=2002&action=admin.user.ban&result=success&page=2&page_size=20&sort=event_time_desc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
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
		req := httptest.NewRequest(http.MethodGet, "/?page_size=1000", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestBuildSystemLogFailureReasonCounts(t *testing.T) {
	rows := []*postgresql.SystemLog{
		{Metadata: map[string]any{"reason": "permission_denied"}},
		{Metadata: map[string]any{"reason": "permission_denied"}},
		{Metadata: map[string]any{"reason": " invalid_payload "}},
		{Metadata: map[string]any{"reason": ""}},
		{Metadata: map[string]any{"reason": 123}},
		{Metadata: nil},
	}

	got := buildSystemLogFailureReasonCounts(rows)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].Key != unknownSystemLogFailureReason || got[0].Count != 3 {
		t.Fatalf("got[0] = %#v, want key=%q,count=3", got[0], unknownSystemLogFailureReason)
	}
	if got[1].Key != "permission_denied" || got[1].Count != 2 {
		t.Fatalf("got[1] = %#v, want key=permission_denied,count=2", got[1])
	}
	if got[2].Key != "invalid_payload" || got[2].Count != 1 {
		t.Fatalf("got[2] = %#v, want key=invalid_payload,count=1", got[2])
	}
}
