package userauth

import (
	"fmt"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiRedis "haruki-suite/utils/database/redis"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	loginRateLimitWindow      = 10 * time.Minute
	loginRateLimitIPLimit     = 30
	loginRateLimitTargetLimit = 8

	loginRateLimitScriptLimitedByNone   = int64(0)
	loginRateLimitScriptLimitedByIP     = int64(1)
	loginRateLimitScriptLimitedByTarget = int64(2)

	loginRateLimitReserveScript = `
local ipCount = redis.call('INCR', KEYS[1])
if ipCount == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[3])
end
local targetCount = redis.call('INCR', KEYS[2])
if targetCount == 1 then
  redis.call('PEXPIRE', KEYS[2], ARGV[3])
end
if ipCount > tonumber(ARGV[1]) then
  return {1, ipCount, targetCount}
end
if targetCount > tonumber(ARGV[2]) then
  return {2, ipCount, targetCount}
end
return {0, ipCount, targetCount}
`

	loginRateLimitReleaseScript = `
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

func respondLoginRateLimited(c fiber.Ctx, key string, message string, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	retryAfter := int64(loginRateLimitWindow.Seconds())
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

func checkLoginRateLimit(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, email string) (limited bool, key string, message string, err error) {
	ctx := c.Context()
	normalizedEmail := platformIdentity.NormalizeEmail(email)
	ipKey := harukiRedis.BuildLoginRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildLoginRateLimitTargetKey(normalizedEmail)

	values, err := apiHelper.DBManager.Redis.Redis.Eval(
		ctx,
		loginRateLimitReserveScript,
		[]string{ipKey, targetKey},
		loginRateLimitIPLimit,
		loginRateLimitTargetLimit,
		loginRateLimitWindow.Milliseconds(),
	).Int64Slice()
	if err != nil {
		return false, "", "", err
	}
	if len(values) != 3 {
		return false, "", "", fmt.Errorf("unexpected login rate limit script result length: %d", len(values))
	}

	limitedBy := values[0]
	switch limitedBy {
	case loginRateLimitScriptLimitedByIP:
		return true, ipKey, "too many login attempts from this IP", nil
	case loginRateLimitScriptLimitedByTarget:
		return true, targetKey, "too many login attempts for this account", nil
	case loginRateLimitScriptLimitedByNone:
		return false, "", "", nil
	default:
		return false, "", "", fmt.Errorf("unexpected login rate limit limitedBy marker: %d", limitedBy)
	}
}

func releaseLoginRateLimitReservation(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, email string) error {
	ctx := c.Context()
	normalizedEmail := platformIdentity.NormalizeEmail(email)
	ipKey := harukiRedis.BuildLoginRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildLoginRateLimitTargetKey(normalizedEmail)
	_, err := apiHelper.DBManager.Redis.Redis.Eval(ctx, loginRateLimitReleaseScript, []string{ipKey, targetKey}).Result()
	return err
}
