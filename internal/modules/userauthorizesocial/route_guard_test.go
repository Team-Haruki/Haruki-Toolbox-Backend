package userauthorizesocial

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestAuthorizeSocialRouteRejectsMismatchedUserID(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Put("/api/user/:toolbox_user_id/authorize-social-platform/:id",
		func(c fiber.Ctx) error {
			c.Locals("userID", "u-100")
			return c.Next()
		},
		userCoreModule.RequireSelfUserParam("toolbox_user_id"),
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
	)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodPut, "/api/user/u-200/authorize-social-platform/1", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}
