package userprofile

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestProfileRoutesRejectMismatchedUserID(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	withSession := func(c fiber.Ctx) error {
		c.Locals("userID", "u-100")
		return c.Next()
	}
	final := func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }

	app.Put("/api/user/:toolbox_user_id/profile", withSession, userCoreModule.RequireSelfUserParam("toolbox_user_id"), final)
	app.Put("/api/user/:toolbox_user_id/change-password", withSession, userCoreModule.RequireSelfUserParam("toolbox_user_id"), final)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodPut, "/api/user/u-200/profile", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("profile status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}

	resp, err = app.Test(httptest.NewRequest(fiber.MethodPut, "/api/user/u-200/change-password", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("change-password status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}
