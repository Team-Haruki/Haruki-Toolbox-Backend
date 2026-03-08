package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseCSVValues(t *testing.T) {
	got := parseCSVValues(" manual,ios_script,manual , , inherit ")
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0] != "manual" || got[1] != "ios_script" || got[2] != "inherit" {
		t.Fatalf("unexpected values: %#v", got)
	}
}

func TestParseUploadMethodsFilter(t *testing.T) {
	if _, err := parseUploadMethodsFilter("manual,invalid"); err == nil {
		t.Fatalf("expected invalid upload_method filter to fail")
	}

	methods, err := parseUploadMethodsFilter("manual,ios_script")
	if err != nil {
		t.Fatalf("parseUploadMethodsFilter returned error: %v", err)
	}
	if len(methods) != 2 {
		t.Fatalf("len(methods) = %d, want 2", len(methods))
	}
}

func TestParseGameUserIDsFilter(t *testing.T) {
	gameUserIDs, err := parseGameUserIDsFilter("123456, 789012,123456")
	if err != nil {
		t.Fatalf("parseGameUserIDsFilter returned error: %v", err)
	}
	if len(gameUserIDs) != 2 {
		t.Fatalf("len(gameUserIDs) = %d, want 2", len(gameUserIDs))
	}
	if gameUserIDs[0] != "123456" || gameUserIDs[1] != "789012" {
		t.Fatalf("unexpected values: %#v", gameUserIDs)
	}
}

func TestParseDataTypesAndServersFilter(t *testing.T) {
	if _, err := parseDataTypesFilter("suite,foo"); err == nil {
		t.Fatalf("expected invalid data_type filter to fail")
	}
	if _, err := parseServersFilter("jp,foo"); err == nil {
		t.Fatalf("expected invalid server filter to fail")
	}

	dataTypes, err := parseDataTypesFilter("suite,mysekai")
	if err != nil {
		t.Fatalf("parseDataTypesFilter returned error: %v", err)
	}
	if len(dataTypes) != 2 {
		t.Fatalf("len(dataTypes) = %d, want 2", len(dataTypes))
	}

	servers, err := parseServersFilter("jp,en")
	if err != nil {
		t.Fatalf("parseServersFilter returned error: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("len(servers) = %d, want 2", len(servers))
	}
}

func TestParseUploadLogSort(t *testing.T) {
	s, err := parseUploadLogSort("")
	if err != nil {
		t.Fatalf("parseUploadLogSort returned error: %v", err)
	}
	if s != defaultUploadLogSort {
		t.Fatalf("sort = %q, want %q", s, defaultUploadLogSort)
	}

	if _, err := parseUploadLogSort("bad_sort"); err == nil {
		t.Fatalf("expected invalid sort to fail")
	}
}

func TestResolveUploadLogTimeRange(t *testing.T) {
	now := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC)

	from, to, err := resolveUploadLogTimeRange("", "", now)
	if err != nil {
		t.Fatalf("resolveUploadLogTimeRange returned error: %v", err)
	}
	if !to.Equal(now) {
		t.Fatalf("to = %s, want %s", to, now)
	}
	if !from.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("from = %s, want %s", from, now.Add(-24*time.Hour))
	}

	if _, _, err := resolveUploadLogTimeRange("2026-03-08T12:00:00Z", "2026-03-08T11:59:59Z", now); err == nil {
		t.Fatalf("expected from >= to to fail")
	}

	if _, _, err := resolveUploadLogTimeRange("2026-01-01T00:00:00Z", "2026-03-08T12:00:00Z", now); err == nil {
		t.Fatalf("expected too-large range to fail")
	}
}

func TestParseUploadLogQueryFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseUploadLogQueryFilters(c, time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-08T00:00:00Z&to=2026-03-08T12:00:00Z&game_user_id=123456,789012&upload_method=manual,ios_script&data_type=suite&server=jp,en&success=true&page=2&page_size=20&sort=upload_time_desc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
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

func TestParseUploadLogQueryFiltersGameUserIDs(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		filters, err := parseUploadLogQueryFilters(c, time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC))
		if err != nil {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		if len(filters.GameUserIDs) != 2 {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if filters.GameUserIDs[0] != "1241241241" || filters.GameUserIDs[1] != "2222" {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/?game_user_id=1241241241,2222,1241241241", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}
