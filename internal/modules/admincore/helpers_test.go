package admincore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestNormalizeRole(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults to user", in: "", want: RoleUser},
		{name: "trim and lower", in: "  ADMIN  ", want: RoleAdmin},
		{name: "super admin", in: "SUPER_ADMIN", want: RoleSuperAdmin},
		{name: "unknown kept normalized", in: "unknown", want: "unknown"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeRole(tc.in)
			if got != tc.want {
				t.Fatalf("NormalizeRole(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsValidRole(t *testing.T) {
	t.Parallel()

	if !IsValidRole(RoleUser) {
		t.Fatalf("RoleUser should be valid")
	}
	if !IsValidRole(" ADMIN ") {
		t.Fatalf("admin should be valid after normalize")
	}
	if !IsValidRole(RoleSuperAdmin) {
		t.Fatalf("RoleSuperAdmin should be valid")
	}
	if IsValidRole("owner") {
		t.Fatalf("owner should be invalid")
	}
}

func TestCurrentAdminActor(t *testing.T) {
	t.Parallel()

	t.Run("valid locals", func(t *testing.T) {
		t.Parallel()
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			c.Locals("userID", "u-1")
			c.Locals("userRole", "ADMIN")
			userID, role, err := CurrentAdminActor(c)
			if err != nil {
				return c.SendStatus(fiber.StatusInternalServerError)
			}
			if userID != "u-1" || role != RoleAdmin {
				return c.SendStatus(fiber.StatusInternalServerError)
			}
			return c.SendStatus(fiber.StatusNoContent)
		})

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("missing user id", func(t *testing.T) {
		t.Parallel()
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			c.Locals("userRole", RoleAdmin)
			_, _, err := CurrentAdminActor(c)
			if err == nil {
				return c.SendStatus(fiber.StatusInternalServerError)
			}
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		})

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})
}

func TestEnsureAdminCanManageTargetUser(t *testing.T) {
	t.Parallel()

	if err := EnsureAdminCanManageTargetUser("a", RoleAdmin, "b", RoleUser); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := EnsureAdminCanManageTargetUser("a", RoleAdmin, "a", RoleUser); err == nil {
		t.Fatalf("expected self-manage error")
	}
	if err := EnsureAdminCanManageTargetUser("a", RoleAdmin, "b", RoleSuperAdmin); err == nil {
		t.Fatalf("expected super admin manage protection error")
	}
	if err := EnsureAdminCanManageTargetUser("a", RoleSuperAdmin, "b", RoleSuperAdmin); err != nil {
		t.Fatalf("super admin should manage super admin, got %v", err)
	}
}

func TestParseOptionalBoolField(t *testing.T) {
	t.Parallel()

	v, err := ParseOptionalBoolField("", "include_revoked")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != nil {
		t.Fatalf("empty value should return nil")
	}

	v, err = ParseOptionalBoolField("true", "include_revoked")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v == nil || !*v {
		t.Fatalf("expected true")
	}

	if _, err = ParseOptionalBoolField("bad", "include_revoked"); err == nil {
		t.Fatalf("invalid bool should fail")
	}
}
