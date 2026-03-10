package userinfo

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestGetSettingsRouteRejectsMismatchedUserID(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/get-settings",
		func(c fiber.Ctx) error {
			c.Locals("userID", "u-100")
			return c.Next()
		},
		userCoreModule.RequireSelfUserParam("toolbox_user_id"),
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
	)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/api/user/u-200/get-settings", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}

func TestGameDataRouteRejectsMismatchedUserID(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/game-data/:server/:data_type/:user_id",
		func(c fiber.Ctx) error {
			c.Locals("userID", "u-100")
			return c.Next()
		},
		userCoreModule.RequireSelfUserParam("toolbox_user_id"),
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
	)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/api/user/u-200/game-data/jp/suite/123", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}
