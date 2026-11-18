package usersocial

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

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

		db := apiHelper.DBManager.DB
		unlock, err := db.LockUserMutations(ctx, userID)
		if err != nil {
			reason = "lock_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve social platform update")
		}
		defer unlock()
		deleted, err := db.ClearSocialLinkAndGrants(ctx, userID)
		if err != nil {
			harukiLogger.Errorf("Failed to clear social platform info: %v", err)
			reason = "clear_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to clear social platform info")
		}
		if deleted == 0 {
			reason = "social_platform_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "no social platform info found")
		}

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "social platform info cleared successfully", nil)
	}
}
