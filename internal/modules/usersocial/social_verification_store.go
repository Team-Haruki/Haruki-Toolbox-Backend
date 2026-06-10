package usersocial

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	socialPlatformVerifyTTL         = 5 * time.Minute
	socialPlatformVerifyMaxAttempts = 5
)

func getSocialPlatformVerifyAttemptCount(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, platform harukiAPIHelper.SocialPlatform, platformUserID string) (int, error) {
	attemptKey := harukiRedis.BuildSocialPlatformVerifyAttemptKey(string(platform), platformUserID)
	var attemptCount int
	found, err := apiHelper.DBManager.Redis.GetCache(c.Context(), attemptKey, &attemptCount)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	return attemptCount, nil
}

func incrementSocialPlatformVerifyAttempt(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, platform harukiAPIHelper.SocialPlatform, platformUserID string) error {
	attemptKey := harukiRedis.BuildSocialPlatformVerifyAttemptKey(string(platform), platformUserID)
	_, err := apiHelper.DBManager.Redis.IncrementWithTTL(c.Context(), attemptKey, socialPlatformVerifyTTL)
	return err
}

func clearSocialPlatformVerifyAttempt(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, platform harukiAPIHelper.SocialPlatform, platformUserID string) {
	attemptKey := harukiRedis.BuildSocialPlatformVerifyAttemptKey(string(platform), platformUserID)
	if err := apiHelper.DBManager.Redis.DeleteCache(c.Context(), attemptKey); err != nil {
		harukiLogger.Warnf("Failed to clear social platform verify attempt key: %v", err)
	}
}
