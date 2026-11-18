package userauthorizesocial

import (
	"net/http/httptest"
	"testing"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"

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

func TestGrantWriteGuardRequiresCurrentLink(t *testing.T) {
	helper := newAuthorizeSocialTestHelper(t)
	db := helper.DBManager.DB
	app := fiber.New()
	app.Post("/:toolbox_user_id", verifyUserHasVerifiedSocialPlatform(helper),
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
	request := func(want int) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(fiber.MethodPost, "/u1", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("grant guard returned %d, want %d", resp.StatusCode, want)
		}
	}
	request(fiber.StatusBadRequest)
	db.SocialPlatformInfo.Create().SetUserID("u1").SetPlatform("qq").SetPlatformUserID("123456").SetVerified(true).ExecX(t.Context())
	request(fiber.StatusNoContent)
}
