package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminGlobalGameBindingSort(t *testing.T) {
	sortValue, err := parseAdminGlobalGameBindingSort("")
	if err != nil {
		t.Fatalf("parseAdminGlobalGameBindingSort returned error: %v", err)
	}
	if sortValue != defaultAdminGlobalGameBindingSort {
		t.Fatalf("sortValue = %q, want %q", sortValue, defaultAdminGlobalGameBindingSort)
	}

	if _, err := parseAdminGlobalGameBindingSort("bad_sort"); err == nil {
		t.Fatalf("expected invalid sort option to fail")
	}
}

func TestParseAdminGlobalGameBindingQueryFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminGlobalGameBindingQueryFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?q=124&server=jp&game_user_id=1241241241&user_id=8532047909&verified=true&page=2&page_size=20&sort=user_id_asc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid server", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?server=us", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid game user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?game_user_id=abc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminGameBindingReassignPayload(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		targetUserID, err := parseAdminGameBindingReassignPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(targetUserID)
	})

	t.Run("camel case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"targetUserId":"1241241241"}`))
		req.Header.Set("Content-Type", "application/json")
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
		if string(body) != "1241241241" {
			t.Fatalf("response body = %q, want %q", string(body), "1241241241")
		}
	})

	t.Run("snake case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"target_user_id":"8532047909"}`))
		req.Header.Set("Content-Type", "application/json")
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
		if string(body) != "8532047909" {
			t.Fatalf("response body = %q, want %q", string(body), "8532047909")
		}
	})

	t.Run("missing target user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestSanitizeAdminBatchGameBindingRefs(t *testing.T) {
	items, err := sanitizeAdminBatchGameBindingRefs([]adminGlobalGameBindingRef{
		{Server: "jp", GameUserID: "123"},
		{Server: "jp", GameUserID: "123"},
		{Server: "en", GameUserIDSnake: "456"},
	})
	if err != nil {
		t.Fatalf("sanitizeAdminBatchGameBindingRefs returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	if _, err := sanitizeAdminBatchGameBindingRefs([]adminGlobalGameBindingRef{
		{Server: "jp", GameUserID: "abc"},
	}); err == nil {
		t.Fatalf("expected invalid game user id to fail")
	}
}

func TestSanitizeAdminBatchGameBindingReassignItems(t *testing.T) {
	items, err := sanitizeAdminBatchGameBindingReassignItems([]adminGlobalGameBindingBatchReassignItem{
		{Server: "jp", GameUserID: "123", TargetUserID: "u1"},
		{Server: "jp", GameUserID: "123", TargetUserID: "u1"},
		{Server: "en", GameUserIDSnake: "456", TargetUserIDSnake: "u2"},
	})
	if err != nil {
		t.Fatalf("sanitizeAdminBatchGameBindingReassignItems returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	if _, err := sanitizeAdminBatchGameBindingReassignItems([]adminGlobalGameBindingBatchReassignItem{
		{Server: "jp", GameUserID: "123", TargetUserID: "u1"},
		{Server: "jp", GameUserID: "123", TargetUserID: "u2"},
	}); err == nil {
		t.Fatalf("expected duplicate binding with conflicting target to fail")
	}
}

func TestAdminGlobalGameBindingRoutePermissions(t *testing.T) {
	app := buildRoleProtectedApp(
		http.MethodPut,
		"/api/admin/game-account-bindings/:server/:game_user_id/reassign",
		[]string{roleAdmin, roleSuperAdmin},
		func(c fiber.Ctx) error {
			return c.SendString(c.Params("server") + "," + c.Params("game_user_id"))
		},
	)

	t.Run("missing session", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/game-account-bindings/jp/12345/reassign", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("user role denied", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/game-account-bindings/jp/12345/reassign", nil)
		req.Header.Set("X-User-ID", "2002")
		req.Header.Set("X-Role", roleUser)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("admin role allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/game-account-bindings/jp/12345/reassign", nil)
		req.Header.Set("X-User-ID", "2002")
		req.Header.Set("X-Role", roleAdmin)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})
}
