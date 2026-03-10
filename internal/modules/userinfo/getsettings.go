package userinfo

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func handleGetSettings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		user, err := apiHelper.DBManager.DB.User.
			Query().
			Where(userSchema.IDEQ(userID)).
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			WithIosScriptCode().
			Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query user settings")
		}
		ud := harukiAPIHelper.BuildUserDataFromDBUser(user, nil)
		return harukiAPIHelper.SuccessResponse(c, "success get latest settings", &ud)
	}
}
