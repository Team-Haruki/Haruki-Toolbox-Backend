package userpasswordreset

import (
	"fmt"
	"strings"
	"time"

	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/gofiber/fiber/v3"
)

const (
	resetPasswordSendRateLimitWindow  = 10 * time.Minute
	resetPasswordSendIPLimit          = 20
	resetPasswordSendTargetLimit      = 3
	resetPasswordApplyRateLimitWindow = 10 * time.Minute
	resetPasswordApplyIPLimit         = 30
	resetPasswordApplyTargetLimit     = 8
	resetPasswordMirrorRetryAttempts  = 3
	resetPasswordMirrorRetryInterval  = 150 * time.Millisecond

	resetPasswordRateLimitScriptLimitedByNone   = int64(0)
	resetPasswordRateLimitScriptLimitedByIP     = int64(1)
	resetPasswordRateLimitScriptLimitedByTarget = int64(2)

	resetPasswordSendRateLimitReserveScript = `
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

	resetPasswordSendRateLimitReleaseScript = `
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

func respondResetPasswordRateLimited(c fiber.Ctx, key string, message string, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	return respondResetPasswordRateLimitedWithWindow(c, key, message, resetPasswordSendRateLimitWindow, apiHelper)
}

func respondResetPasswordRateLimitedWithWindow(
	c fiber.Ctx,
	key string,
	message string,
	window time.Duration,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
) error {
	retryAfter := int64(window.Seconds())
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

func checkResetPasswordSendRateLimit(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, email string) (limited bool, key string, message string, err error) {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	email = platformIdentity.NormalizeEmail(email)
	ipKey := harukiRedis.BuildResetPasswordSendRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildResetPasswordSendRateLimitTargetKey(email)
	values, err := apiHelper.DBManager.Redis.Redis.Eval(
		ctx,
		resetPasswordSendRateLimitReserveScript,
		[]string{ipKey, targetKey},
		resetPasswordSendIPLimit,
		resetPasswordSendTargetLimit,
		resetPasswordSendRateLimitWindow.Milliseconds(),
	).Int64Slice()
	if err != nil {
		harukiLogger.Errorf("Failed to reserve reset-password send rate limit: %v", err)
		return false, "", "", err
	}
	if len(values) != 3 {
		return false, "", "", fmt.Errorf("unexpected reset-password send rate limit script result length: %d", len(values))
	}

	switch values[0] {
	case resetPasswordRateLimitScriptLimitedByIP:
		return true, ipKey, "too many reset requests from this IP", nil
	case resetPasswordRateLimitScriptLimitedByTarget:
		return true, targetKey, "too many reset emails sent to this address", nil
	case resetPasswordRateLimitScriptLimitedByNone:
		return false, "", "", nil
	default:
		return false, "", "", fmt.Errorf("unexpected reset-password send rate limit limitedBy marker: %d", values[0])
	}
}

func releaseResetPasswordSendRateLimitReservation(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, email string) error {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	email = platformIdentity.NormalizeEmail(email)
	ipKey := harukiRedis.BuildResetPasswordSendRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildResetPasswordSendRateLimitTargetKey(email)
	_, err := apiHelper.DBManager.Redis.Redis.Eval(ctx, resetPasswordSendRateLimitReleaseScript, []string{ipKey, targetKey}).Result()
	return err
}

func checkResetPasswordApplyRateLimit(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, target string) (limited bool, key string, message string, err error) {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	target = strings.TrimSpace(target)
	if normalizedEmail := platformIdentity.NormalizeEmail(target); normalizedEmail != "" && strings.Contains(normalizedEmail, "@") {
		target = normalizedEmail
	} else {
		// In recovery-code-only flows, avoid per-code key bypass by binding target bucket to source IP.
		target = "ip:" + strings.TrimSpace(clientIP)
	}
	ipKey := harukiRedis.BuildResetPasswordApplyRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildResetPasswordApplyRateLimitTargetKey(target)
	values, err := apiHelper.DBManager.Redis.Redis.Eval(
		ctx,
		resetPasswordSendRateLimitReserveScript,
		[]string{ipKey, targetKey},
		resetPasswordApplyIPLimit,
		resetPasswordApplyTargetLimit,
		resetPasswordApplyRateLimitWindow.Milliseconds(),
	).Int64Slice()
	if err != nil {
		harukiLogger.Errorf("Failed to reserve reset-password apply rate limit: %v", err)
		return false, "", "", err
	}
	if len(values) != 3 {
		return false, "", "", fmt.Errorf("unexpected reset-password apply rate limit script result length: %d", len(values))
	}

	switch values[0] {
	case resetPasswordRateLimitScriptLimitedByIP:
		return true, ipKey, "too many password reset attempts from this IP", nil
	case resetPasswordRateLimitScriptLimitedByTarget:
		return true, targetKey, "too many password reset attempts for this target", nil
	case resetPasswordRateLimitScriptLimitedByNone:
		return false, "", "", nil
	default:
		return false, "", "", fmt.Errorf("unexpected reset-password apply rate limit limitedBy marker: %d", values[0])
	}
}
