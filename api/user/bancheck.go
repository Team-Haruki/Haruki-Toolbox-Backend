package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

// checkUserNotBanned is a middleware that checks if the current user is banned.
// It should be used after VerifySessionToken middleware which sets userID in Locals.
func checkUserNotBanned(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, ok := c.Locals("userID").(string)
		if !ok || userID == "" {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		ctx := c.Context()
		user, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Select(userSchema.FieldBanned, userSchema.FieldBanReason).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "user not found")
		}

		if user.Banned {
			banMessage := "Your account has been banned"
			if user.BanReason != nil && *user.BanReason != "" {
				banMessage = "Your account has been banned: " + *user.BanReason
			}
			return harukiAPIHelper.ErrorForbidden(c, banMessage)
		}

		return c.Next()
	}
}
