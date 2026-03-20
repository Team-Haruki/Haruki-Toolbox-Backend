package useremail

import (
	"crypto/rand"
	"fmt"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/smtp"
	"math/big"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	emailSendRateLimitWindow = 10 * time.Minute

	emailSendIPLimit     = 20
	emailSendTargetLimit = 3

	emailRateLimitScriptLimitedByNone   = int64(0)
	emailRateLimitScriptLimitedByIP     = int64(1)
	emailRateLimitScriptLimitedByTarget = int64(2)

	emailSendRateLimitReserveScript = `
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

	emailSendRateLimitReleaseScript = `
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

func GenerateCode(antiCensor bool) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("failed to generate random code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	if antiCensor {
		return strings.Join(strings.Split(code, ""), "/"), nil
	}
	return code, nil
}

func respondEmailSendRateLimited(c fiber.Ctx, key string, message string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	retryAfter := int64(emailSendRateLimitWindow.Seconds())
	if helper != nil && helper.DBManager != nil && helper.DBManager.Redis != nil && helper.DBManager.Redis.Redis != nil {
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

func checkSendEmailRateLimit(c fiber.Ctx, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, email string) (limited bool, key string, message string, err error) {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	email = platformIdentity.NormalizeEmail(email)
	ipKey := harukiRedis.BuildEmailVerifySendRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildEmailVerifySendRateLimitTargetKey(email)
	values, err := helper.DBManager.Redis.Redis.Eval(
		ctx,
		emailSendRateLimitReserveScript,
		[]string{ipKey, targetKey},
		emailSendIPLimit,
		emailSendTargetLimit,
		emailSendRateLimitWindow.Milliseconds(),
	).Int64Slice()
	if err != nil {
		harukiLogger.Errorf("Failed to reserve email send rate limit: %v", err)
		return false, "", "", err
	}
	if len(values) != 3 {
		return false, "", "", fmt.Errorf("unexpected email send rate limit script result length: %d", len(values))
	}

	switch values[0] {
	case emailRateLimitScriptLimitedByIP:
		return true, ipKey, "too many requests from this IP", nil
	case emailRateLimitScriptLimitedByTarget:
		return true, targetKey, "too many verification emails sent to this address", nil
	case emailRateLimitScriptLimitedByNone:
		return false, "", "", nil
	default:
		return false, "", "", fmt.Errorf("unexpected email send rate limit limitedBy marker: %d", values[0])
	}
}

func releaseSendEmailRateLimitReservation(c fiber.Ctx, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientIP, email string) error {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	email = platformIdentity.NormalizeEmail(email)
	ipKey := harukiRedis.BuildEmailVerifySendRateLimitIPKey(clientIP)
	targetKey := harukiRedis.BuildEmailVerifySendRateLimitTargetKey(email)
	_, err := helper.DBManager.Redis.Redis.Eval(ctx, emailSendRateLimitReleaseScript, []string{ipKey, targetKey}).Result()
	return err
}

func validateAndReserveEmailSend(c fiber.Ctx, email, challengeToken string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) (string, error) {
	clientIP := c.IP()
	resp, err := cloudflare.ValidateTurnstile(challengeToken, clientIP)
	if err != nil {
		return "", harukiAPIHelper.ErrorInternal(c, "captcha service unavailable")
	}
	if resp == nil || !resp.Success {
		return "", harukiAPIHelper.ErrorBadRequest(c, "captcha verify failed")
	}
	limited, limitKey, limitMessage, err := checkSendEmailRateLimit(c, helper, clientIP, email)
	if err != nil {
		return "", harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
	}
	if limited {
		return "", respondEmailSendRateLimited(c, limitKey, limitMessage, helper)
	}
	return clientIP, nil
}

func sendVerificationCode(c fiber.Ctx, email string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	code, err := GenerateCode(false)
	if err != nil {
		harukiLogger.Errorf("Failed to generate code: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to generate verification code")
	}
	redisKey := harukiRedis.BuildEmailVerifyKey(email)
	if err := helper.DBManager.Redis.SetCache(ctx, redisKey, code, 5*time.Minute); err != nil {
		harukiLogger.Errorf("Failed to set redis cache: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to save code")
	}
	body := strings.ReplaceAll(smtp.VerificationCodeTemplate, "{{CODE}}", code)
	if err := helper.SMTPClient.Send([]string{email}, "您的验证码 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
		if delErr := helper.DBManager.Redis.DeleteCache(ctx, redisKey); delErr != nil {
			harukiLogger.Warnf("Failed to rollback verification code for %s: %v", email, delErr)
		}
		harukiLogger.Errorf("Failed to send email: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to send email")
	}
	return nil
}

func SendEmailHandler(c fiber.Ctx, email, challengeToken string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	email = platformIdentity.NormalizeEmail(email)
	if email == "" {
		return harukiAPIHelper.ErrorBadRequest(c, "email is required")
	}
	clientIP, err := validateAndReserveEmailSend(c, email, challengeToken, helper)
	if err != nil {
		return err
	}
	if err := sendVerificationCode(c, email, helper); err != nil {
		if releaseErr := releaseSendEmailRateLimitReservation(c, helper, clientIP, email); releaseErr != nil {
			harukiLogger.Warnf("Failed to release email send rate limit reservation for %s: %v", email, releaseErr)
		}
		return err
	}
	return harukiAPIHelper.SuccessResponse[string](c, "verification code sent", nil)
}

func VerifyEmailHandler(c fiber.Ctx, email, oneTimePassword string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) (bool, error) {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	email = platformIdentity.NormalizeEmail(email)
	if email == "" {
		return false, harukiAPIHelper.ErrorBadRequest(c, "email is required")
	}
	attemptKey := harukiRedis.BuildOTPAttemptKey(email)
	var attemptCount int
	found, err := helper.DBManager.Redis.GetCache(ctx, attemptKey, &attemptCount)
	if err != nil {
		harukiLogger.Errorf("Failed to get OTP attempt count: %v", err)
		return false, harukiAPIHelper.ErrorInternal(c, "Verification service unavailable")
	}
	if found && attemptCount >= 5 {
		return false, harukiAPIHelper.ErrorBadRequest(c, "Too many verification attempts. Please request a new code.")
	}
	redisKey := harukiRedis.BuildEmailVerifyKey(email)
	var code string
	found, err = helper.DBManager.Redis.GetCache(ctx, redisKey, &code)
	if err != nil {
		harukiLogger.Errorf("Failed to get redis cache: %v", err)
		return false, harukiAPIHelper.ErrorInternal(c, "Verification service unavailable")
	}
	if !found {
		return false, harukiAPIHelper.ErrorBadRequest(c, "verification code expired or not found")
	}
	if oneTimePassword != code {
		newCount := attemptCount + 1
		if err := helper.DBManager.Redis.SetCache(ctx, attemptKey, newCount, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to update OTP attempt count: %v", err)
			return false, harukiAPIHelper.ErrorInternal(c, "Verification service unavailable")
		}
		return false, harukiAPIHelper.ErrorBadRequest(c, "invalid verification code")
	}
	consumed, err := helper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, redisKey, code)
	if err != nil {
		harukiLogger.Errorf("Failed to consume verification code: %v", err)
		return false, harukiAPIHelper.ErrorInternal(c, "Verification service unavailable")
	}
	if !consumed {
		return false, harukiAPIHelper.ErrorBadRequest(c, "verification code expired or not found")
	}
	if err := helper.DBManager.Redis.DeleteCache(ctx, attemptKey); err != nil {
		harukiLogger.Warnf("Failed to clear OTP attempt key for %s: %v", email, err)
	}
	return true, nil
}
