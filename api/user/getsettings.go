package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func handleGetSettings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)

		user, err := apiHelper.DBManager.DB.User.
			Query().
			Where(userSchema.IDEQ(userID)).
			WithEmailInfo().
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			WithIosScriptCode().
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "User not found")
		}

		ud := harukiAPIHelper.BuildUserDataFromDBUser(user, nil)
		return harukiAPIHelper.SuccessResponse(c, "success get latest settings", &ud)
	}
}

func registerGetInfoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Get("/api/user/:toolbox_user_id/get-settings", apiHelper.SessionHandler.VerifySessionToken, handleGetSettings(apiHelper))
}
