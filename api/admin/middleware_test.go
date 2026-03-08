package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestNormalizeRole(t *testing.T) {
	t.Run("empty defaults to user", func(t *testing.T) {
		if got := normalizeRole(""); got != roleUser {
			t.Fatalf("normalizeRole(\"\") = %q, want %q", got, roleUser)
		}
	})

	t.Run("trim and lowercase", func(t *testing.T) {
		if got := normalizeRole("  SUPER_Admin "); got != roleSuperAdmin {
			t.Fatalf("normalizeRole returned %q, want %q", got, roleSuperAdmin)
		}
	})
}

func TestIsValidRole(t *testing.T) {
	if !isValidRole(roleUser) || !isValidRole(roleAdmin) || !isValidRole(roleSuperAdmin) {
		t.Fatalf("expected built-in roles to be valid")
	}
	if isValidRole("owner") {
		t.Fatalf("expected unknown role to be invalid")
	}
}

func TestRequireAnyRole(t *testing.T) {
	makeApp := func(lookup userRoleLookup, allowedRoles ...string) *fiber.App {
		app := fiber.New()
		app.Get("/",
			func(c fiber.Ctx) error {
				if userID := c.Get("X-User-ID"); userID != "" {
					c.Locals("userID", userID)
				}
				return c.Next()
			},
			requireAnyRole(nil, lookup, allowedRoles...),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)
		return app
	}

	t.Run("missing user session", func(t *testing.T) {
		app := makeApp(func(c fiber.Ctx, userID string) (string, bool, error) {
			return roleAdmin, false, nil
		}, roleAdmin)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("lookup failure treated as invalid session", func(t *testing.T) {
		app := makeApp(func(c fiber.Ctx, userID string) (string, bool, error) {
			return "", false, errors.New("query failed")
		}, roleAdmin)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User-ID", "1001")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("banned user denied", func(t *testing.T) {
		app := makeApp(func(c fiber.Ctx, userID string) (string, bool, error) {
			return roleSuperAdmin, true, nil
		}, roleSuperAdmin)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User-ID", "1001")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("insufficient role", func(t *testing.T) {
		app := makeApp(func(c fiber.Ctx, userID string) (string, bool, error) {
			return roleUser, false, nil
		}, roleAdmin)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User-ID", "1001")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("admin role allowed", func(t *testing.T) {
		app := makeApp(func(c fiber.Ctx, userID string) (string, bool, error) {
			return roleAdmin, false, nil
		}, roleAdmin)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User-ID", "1001")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("super admin satisfies admin gate", func(t *testing.T) {
		app := makeApp(func(c fiber.Ctx, userID string) (string, bool, error) {
			return roleSuperAdmin, false, nil
		}, roleAdmin, roleSuperAdmin)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User-ID", "1001")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("admin denied by super admin gate", func(t *testing.T) {
		app := makeApp(func(c fiber.Ctx, userID string) (string, bool, error) {
			return roleAdmin, false, nil
		}, roleSuperAdmin)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User-ID", "1001")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})
}
