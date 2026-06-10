package harukibotneo

import (
	"fmt"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"time"

	"github.com/gofiber/fiber/v3"
)

func checkSendMailRateLimit(c fiber.Ctx, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, qq string) (limited bool, key string, message string, err error) {
	ctx := c.Context()
	ipKey := harukiRedis.BuildBotSendMailRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildBotSendMailRateLimitTargetKey(qq)
	values, err := helper.DBManager.Redis.Redis.Eval(
		ctx,
		sendMailRateLimitScript,
		[]string{ipKey, targetKey},
		sendMailIPLimit,
		sendMailTargetLimit,
		sendMailRateLimitWindow.Milliseconds(),
	).Int64Slice()
	if err != nil {
		harukiLogger.Errorf("Failed to check send mail rate limit: %v", err)
		return false, "", "", err
	}
	if len(values) != 3 {
		return false, "", "", fmt.Errorf("unexpected rate limit script result length: %d", len(values))
	}

	switch values[0] {
	case rateLimitLimitedByIP:
		return true, ipKey, "too many requests from this IP", nil
	case rateLimitLimitedByTarget:
		return true, targetKey, "too many verification emails sent to this QQ", nil
	case rateLimitLimitedByNone:
		return false, "", "", nil
	default:
		return false, "", "", fmt.Errorf("unexpected rate limit marker: %d", values[0])
	}
}

func releaseSendMailRateLimit(c fiber.Ctx, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, qq string) {
	ctx := c.Context()
	ipKey := harukiRedis.BuildBotSendMailRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildBotSendMailRateLimitTargetKey(qq)
	_, err := helper.DBManager.Redis.Redis.Eval(ctx, sendMailRateLimitReleaseScript, []string{ipKey, targetKey}).Result()
	if err != nil {
		harukiLogger.Warnf("Failed to release send mail rate limit reservation: %v", err)
	}
}

func respondRateLimited(c fiber.Ctx, key, message string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, window time.Duration) error {
	retryAfter := int64(window.Seconds())
	if helper.DBManager != nil && helper.DBManager.Redis != nil && helper.DBManager.Redis.Redis != nil {
		if ttl, err := helper.DBManager.Redis.Redis.TTL(c.Context(), key).Result(); err == nil && ttl > 0 {
			retryAfter = int64(ttl.Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
	}
	c.Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusTooManyRequests, fmt.Sprintf("%s (retry after %ds)", message, retryAfter), nil)
}
