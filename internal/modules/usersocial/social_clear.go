package usersocial

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func handleClearSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.social_platform.clear", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Query().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(userID))).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query social platform info: %v", err)
			reason = "query_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}
		if !exists {
			reason = "social_platform_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "no social platform info found")
		}

		_, err = apiHelper.DBManager.DB.SocialPlatformInfo.
			Delete().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(userID))).
			Exec(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to delete social platform info: %v", err)
			reason = "clear_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to clear social platform info")
		}

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "social platform info cleared successfully", nil)
	}
}
