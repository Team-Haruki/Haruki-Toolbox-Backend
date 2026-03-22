package user

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestIOSUploadCodeRouteRejectsMismatchedUserID(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Post("/api/user/:toolbox_user_id/ios/generate-upload-code",
		func(c fiber.Ctx) error {
			c.Locals("userID", "u-100")
			return c.Next()
		},
		userCoreModule.RequireSelfUserParam("toolbox_user_id"),
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
	)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodPost, "/api/user/u-200/ios/generate-upload-code", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}
