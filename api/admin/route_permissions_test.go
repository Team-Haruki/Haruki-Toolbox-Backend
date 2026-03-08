package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func buildRoleProtectedApp(method string, path string, allowedRoles []string, handler fiber.Handler) *fiber.App {
	lookup := func(c fiber.Ctx, userID string) (string, bool, error) {
		role := strings.TrimSpace(c.Get("X-Role"))
		if role == "" {
			role = roleUser
		}
		return role, false, nil
	}

	withSession := func(c fiber.Ctx) error {
		if userID := strings.TrimSpace(c.Get("X-User-ID")); userID != "" {
			c.Locals("userID", userID)
		}
		return c.Next()
	}

	app := fiber.New()
	middlewares := []fiber.Handler{
		withSession,
		requireAnyRole(nil, lookup, allowedRoles...),
		handler,
	}

	switch method {
	case http.MethodGet:
		app.Get(path, middlewares[0], middlewares[1], middlewares[2])
	case http.MethodPost:
		app.Post(path, middlewares[0], middlewares[1], middlewares[2])
	case http.MethodPut:
		app.Put(path, middlewares[0], middlewares[1], middlewares[2])
	case http.MethodDelete:
		app.Delete(path, middlewares[0], middlewares[1], middlewares[2])
	default:
		panic("unsupported method")
	}
	return app
}

func TestAdminUserIntegrationRoutePermissionsAndBehavior(t *testing.T) {
	app := buildRoleProtectedApp(
		http.MethodPut,
		"/api/admin/users/:target_user_id/game-account-bindings/:server/:game_user_id",
		[]string{roleAdmin, roleSuperAdmin},
		func(c fiber.Ctx) error {
			return c.SendString(c.Params("target_user_id") + "," + c.Params("server") + "," + c.Params("game_user_id"))
		},
	)

	t.Run("missing session", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/users/1001/game-account-bindings/jp/12345", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("user role denied", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/users/1001/game-account-bindings/jp/12345", nil)
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

	t.Run("admin role allowed with params", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/users/1001/game-account-bindings/jp/12345", nil)
		req.Header.Set("X-User-ID", "2002")
		req.Header.Set("X-Role", roleAdmin)
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
		if string(body) != "1001,jp,12345" {
			t.Fatalf("response body = %q, want %q", string(body), "1001,jp,12345")
		}
	})

	t.Run("super admin role allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/users/1001/game-account-bindings/jp/12345", nil)
		req.Header.Set("X-User-ID", "2002")
		req.Header.Set("X-Role", roleSuperAdmin)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})
}

func TestAdminContentRoutePermissions(t *testing.T) {
	app := buildRoleProtectedApp(
		http.MethodPost,
		"/api/admin/content/friend-links",
		[]string{roleAdmin, roleSuperAdmin},
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
	)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/content/friend-links", nil)
	req.Header.Set("X-User-ID", "1001")
	req.Header.Set("X-Role", roleUser)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/admin/content/friend-links", nil)
	req.Header.Set("X-User-ID", "1001")
	req.Header.Set("X-Role", roleAdmin)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}

func TestAdminSuperAdminOnlyRoutePermissions(t *testing.T) {
	app := buildRoleProtectedApp(
		http.MethodPut,
		"/api/admin/config/public-api-keys",
		[]string{roleSuperAdmin},
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
	)

	req := httptest.NewRequest(http.MethodPut, "/api/admin/config/public-api-keys", nil)
	req.Header.Set("X-User-ID", "1001")
	req.Header.Set("X-Role", roleAdmin)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/admin/config/public-api-keys", nil)
	req.Header.Set("X-User-ID", "1001")
	req.Header.Set("X-Role", roleSuperAdmin)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}
