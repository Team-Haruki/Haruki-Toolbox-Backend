package usersocial

import (
	"fmt"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	qqMailSendRateLimitWindow = 10 * time.Minute
	qqMailSendUserLimit       = 10
	qqMailSendTargetLimit     = 3

	socialRateLimitScriptLimitedByNone   = int64(0)
	socialRateLimitScriptLimitedByUser   = int64(1)
	socialRateLimitScriptLimitedByTarget = int64(2)

	socialRateLimitReserveScript = `
local userCount = redis.call('INCR', KEYS[1])
if userCount == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[3])
end
local targetCount = redis.call('INCR', KEYS[2])
if targetCount == 1 then
  redis.call('PEXPIRE', KEYS[2], ARGV[3])
end
if userCount > tonumber(ARGV[1]) then
  return {1, userCount, targetCount}
end
if targetCount > tonumber(ARGV[2]) then
  return {2, userCount, targetCount}
end
return {0, userCount, targetCount}
`

	socialRateLimitReleaseScript = `
for i=1,#KEYS do
  local current = redis.call('GET', KEYS[i])
  if current then
    local num = tonumber(current)
    if num == nil or num <= 1 then
      redis.call('DEL', KEYS[i])
    else
      redis.call('DECR', KEYS[i])
    end
  end
end
return 1
`
)

func respondQQMailRateLimited(c fiber.Ctx, key string, message string, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	retryAfter := int64(qqMailSendRateLimitWindow.Seconds())
	if apiHelper != nil && apiHelper.DBManager != nil && apiHelper.DBManager.Redis != nil && apiHelper.DBManager.Redis.Redis != nil {
		if ttl, err := apiHelper.DBManager.Redis.Redis.TTL(c.Context(), key).Result(); err == nil && ttl > 0 {
			retryAfter = int64(ttl.Seconds())
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
	}
	c.Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusTooManyRequests, fmt.Sprintf("%s (retry after %ds)", message, retryAfter), nil)
}

func checkQQMailSendRateLimit(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, qq string) (limited bool, key string, message string, err error) {
	userKey := harukiRedis.BuildQQMailSendRateLimitUserKey(userID)
	targetKey := harukiRedis.BuildQQMailSendRateLimitTargetKey(qq)
	values, err := apiHelper.DBManager.Redis.Redis.Eval(
		c.Context(),
		socialRateLimitReserveScript,
		[]string{userKey, targetKey},
		qqMailSendUserLimit,
		qqMailSendTargetLimit,
		qqMailSendRateLimitWindow.Milliseconds(),
	).Int64Slice()
	if err != nil {
		harukiLogger.Errorf("Failed to reserve QQ mail send rate limit: %v", err)
		return false, "", "", err
	}
	if len(values) != 3 {
		return false, "", "", fmt.Errorf("unexpected QQ mail send rate limit script result length: %d", len(values))
	}

	switch values[0] {
	case socialRateLimitScriptLimitedByUser:
		return true, userKey, "too many QQ verification emails sent by this user", nil
	case socialRateLimitScriptLimitedByTarget:
		return true, targetKey, "too many QQ verification emails sent to this QQ number", nil
	case socialRateLimitScriptLimitedByNone:
		return false, "", "", nil
	default:
		return false, "", "", fmt.Errorf("unexpected QQ mail send rate limit limitedBy marker: %d", values[0])
	}
}

func releaseQQMailSendRateLimitReservation(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, qq string) error {
	userKey := harukiRedis.BuildQQMailSendRateLimitUserKey(userID)
	targetKey := harukiRedis.BuildQQMailSendRateLimitTargetKey(qq)
	_, err := apiHelper.DBManager.Redis.Redis.Eval(c.Context(), socialRateLimitReleaseScript, []string{userKey, targetKey}).Result()
	return err
}
