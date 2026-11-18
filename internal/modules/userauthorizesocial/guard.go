package userauthorizesocial

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func verifyUserHasVerifiedSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		ctx := c.Context()
		db := apiHelper.DBManager.DB
		unlock, err := db.LockUserMutations(ctx, toolboxUserID)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve social authorization")
		}
		defer unlock()
		info, err := db.SocialPlatformInfo.Query().
			Where(
				socialplatforminfo.UserSocialPlatformInfoEQ(toolboxUserID),
				socialplatforminfo.Verified(true),
			).First(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorBadRequest(c, "user has no verified social platform info")
			}
			harukiLogger.Errorf("Failed to query verified social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}
		if info == nil {
			return harukiAPIHelper.ErrorBadRequest(c, "user has no verified social platform info")
		}
		return c.Next()
	}
}

func isSupportedSocialPlatform(platform harukiAPIHelper.SocialPlatform) bool {
	switch platform {
	case harukiAPIHelper.SocialPlatformQQ,
		harukiAPIHelper.SocialPlatformQQBot,
		harukiAPIHelper.SocialPlatformDiscord,
		harukiAPIHelper.SocialPlatformTelegram:
		return true
	default:
		return false
	}
}
