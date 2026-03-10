package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	"haruki-suite/utils/database/postgresql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func requireFiberErrorCode(t *testing.T, err error, wantCode int) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	fiberErr, ok := err.(*fiber.Error)
	if !ok {
		t.Fatalf("error type = %T, want *fiber.Error", err)
	}
	if fiberErr.Code != wantCode {
		t.Fatalf("error code = %d, want %d", fiberErr.Code, wantCode)
	}
}

func TestParseAdminUsersSort(t *testing.T) {
	t.Run("default sort when empty", func(t *testing.T) {
		sortValue, err := parseAdminUsersSort("")
		if err != nil {
			t.Fatalf("parseAdminUsersSort returned error: %v", err)
		}
		if sortValue != defaultAdminUsersSort {
			t.Fatalf("sortValue = %q, want %q", sortValue, defaultAdminUsersSort)
		}
	})

	t.Run("accept valid sort", func(t *testing.T) {
		sortValue, err := parseAdminUsersSort(adminUsersSortNameAsc)
		if err != nil {
			t.Fatalf("parseAdminUsersSort returned error: %v", err)
		}
		if sortValue != adminUsersSortNameAsc {
			t.Fatalf("sortValue = %q, want %q", sortValue, adminUsersSortNameAsc)
		}
	})

	t.Run("accept created at sort", func(t *testing.T) {
		sortValue, err := parseAdminUsersSort(adminUsersSortCreatedAtDesc)
		if err != nil {
			t.Fatalf("parseAdminUsersSort returned error: %v", err)
		}
		if sortValue != adminUsersSortCreatedAtDesc {
			t.Fatalf("sortValue = %q, want %q", sortValue, adminUsersSortCreatedAtDesc)
		}
	})

	t.Run("reject invalid sort", func(t *testing.T) {
		if _, err := parseAdminUsersSort("random"); err == nil {
			t.Fatalf("expected invalid sort to fail")
		}
	})
}

func TestParseAdminUserQueryFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminUserQueryFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("accept valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?q=test&role=admin&banned=true&allow_cn_mysekai=true&created_from=2026-03-01T00:00:00Z&created_to=2026-03-08T00:00:00Z&page=2&page_size=20&sort=created_at_desc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("reject invalid role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?role=owner", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid banned filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?banned=not_bool", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid allow_cn_mysekai filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?allow_cn_mysekai=not_bool", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("accept camel case allow filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?allowCNMysekai=false", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("reject oversized page_size", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page_size=999", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid created_from", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?created_from=not-a-time", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject reversed created range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?created_from=2026-03-09T00:00:00Z&created_to=2026-03-08T00:00:00Z", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestEnsureAdminCanManageTargetUser(t *testing.T) {
	t.Run("super admin can manage super admin", func(t *testing.T) {
		if err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleSuperAdmin, "2", roleSuperAdmin); err != nil {
			t.Fatalf("ensureAdminCanManageTargetUser returned error: %v", err)
		}
	})

	t.Run("admin cannot manage super admin", func(t *testing.T) {
		err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleAdmin, "2", roleSuperAdmin)
		requireFiberErrorCode(t, err, fiber.StatusForbidden)
	})

	t.Run("admin can manage normal admin", func(t *testing.T) {
		if err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleAdmin, "2", roleAdmin); err != nil {
			t.Fatalf("ensureAdminCanManageTargetUser returned error: %v", err)
		}
	})

	t.Run("cannot manage self", func(t *testing.T) {
		err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleSuperAdmin, "1", roleUser)
		requireFiberErrorCode(t, err, fiber.StatusBadRequest)
	})
}

func TestSanitizeBanReason(t *testing.T) {
	t.Run("nil reason", func(t *testing.T) {
		reason, err := sanitizeBanReason(nil)
		if err != nil {
			t.Fatalf("sanitizeBanReason returned error: %v", err)
		}
		if reason != nil {
			t.Fatalf("reason = %v, want nil", reason)
		}
	})

	t.Run("empty reason", func(t *testing.T) {
		raw := "   "
		reason, err := sanitizeBanReason(&raw)
		if err != nil {
			t.Fatalf("sanitizeBanReason returned error: %v", err)
		}
		if reason != nil {
			t.Fatalf("reason = %v, want nil", reason)
		}
	})

	t.Run("trim valid reason", func(t *testing.T) {
		raw := "  spam upload  "
		reason, err := sanitizeBanReason(&raw)
		if err != nil {
			t.Fatalf("sanitizeBanReason returned error: %v", err)
		}
		if reason == nil || *reason != "spam upload" {
			t.Fatalf("reason = %v, want \"spam upload\"", reason)
		}
	})

	t.Run("reject too long reason", func(t *testing.T) {
		raw := strings.Repeat("a", 501)
		_, err := sanitizeBanReason(&raw)
		requireFiberErrorCode(t, err, fiber.StatusBadRequest)
	})
}

func TestBuildAdminUserListItems(t *testing.T) {
	created := time.Date(2026, 3, 8, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	rows := []*postgresql.User{
		{
			ID:             "1001",
			Name:           "alice",
			Email:          "alice@example.com",
			Role:           "admin",
			Banned:         false,
			AllowCnMysekai: true,
			CreatedAt:      &created,
		},
		{
			ID:             "1002",
			Name:           "bob",
			Email:          "bob@example.com",
			Role:           "user",
			Banned:         true,
			AllowCnMysekai: false,
		},
	}

	items := buildAdminUserListItems(rows)
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].CreatedAt == nil {
		t.Fatalf("items[0].CreatedAt = nil, want non-nil")
	}
	if items[0].CreatedAt.Location() != time.UTC {
		t.Fatalf("items[0].CreatedAt location = %s, want UTC", items[0].CreatedAt.Location())
	}
	if got := items[0].CreatedAt.Format(time.RFC3339); got != "2026-03-08T01:30:00Z" {
		t.Fatalf("items[0].CreatedAt = %s, want 2026-03-08T01:30:00Z", got)
	}
	if items[1].CreatedAt != nil {
		t.Fatalf("items[1].CreatedAt = %v, want nil", items[1].CreatedAt)
	}
	if !items[0].AllowCNMysekai {
		t.Fatalf("items[0].AllowCNMysekai = false, want true")
	}
	if items[1].AllowCNMysekai {
		t.Fatalf("items[1].AllowCNMysekai = true, want false")
	}
}
