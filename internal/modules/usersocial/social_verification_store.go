package usersocial

import (
	"context"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

const (
	socialPlatformVerifyTTL         = 5 * time.Minute
	socialPlatformVerifyMaxAttempts = 5
)

func reserveSocialPlatformVerifyAttempt(
	ctx context.Context,
	redisManager *harukiRedis.HarukiRedisManager,
	platform harukiAPIHelper.SocialPlatform,
	platformUserID string,
) (bool, error) {
	attemptKey := harukiRedis.BuildSocialPlatformVerifyAttemptKey(string(platform), platformUserID)
	attemptCount, err := redisManager.IncrementWithTTL(ctx, attemptKey, socialPlatformVerifyTTL)
	if err != nil {
		return false, err
	}
	return attemptCount > int64(socialPlatformVerifyMaxAttempts), nil
}

func clearSocialPlatformVerifyAttempt(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, platform harukiAPIHelper.SocialPlatform, platformUserID string) {
	attemptKey := harukiRedis.BuildSocialPlatformVerifyAttemptKey(string(platform), platformUserID)
	if err := apiHelper.DBManager.Redis.DeleteCache(c.Context(), attemptKey); err != nil {
		harukiLogger.Warnf("Failed to clear social platform verify attempt key: %v", err)
	}
}
