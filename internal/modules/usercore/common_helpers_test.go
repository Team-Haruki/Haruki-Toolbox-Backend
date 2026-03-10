package usercore

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRequireSelfUserParam(t *testing.T) {
	t.Parallel()

	t.Run("allow when target matches authenticated user", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:toolbox_user_id/resource",
			func(c fiber.Ctx) error {
				c.Locals("userID", "u-123")
				return c.Next()
			},
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-123/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("reject when target does not match authenticated user", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:toolbox_user_id/resource",
			func(c fiber.Ctx) error {
				c.Locals("userID", "u-123")
				return c.Next()
			},
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-456/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("reject when user is not authenticated", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:toolbox_user_id/resource",
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-123/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("reject when configured param does not exist", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:other/resource",
			func(c fiber.Ctx) error {
				c.Locals("userID", "u-123")
				return c.Next()
			},
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-123/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}
