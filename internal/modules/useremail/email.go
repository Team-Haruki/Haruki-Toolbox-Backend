package useremail

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformIdentity "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/identity"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailtemplate"
	harukiCloudflare "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	emailCodeTTL = 5 * time.Minute

	emailSendRateLimitWindow = 10 * time.Minute

	emailSendUserLimit    = 10
	emailSendIPLimit      = 20
	emailSendTargetLimit  = 3
	otpVerifyPerCodeLimit = 5
	otpVerifyIPWindow     = 10 * time.Minute
	otpVerifyPerIPLimit   = 50

	emailSendRateLimitReserveScript = `
for i = 1, #KEYS do
  local count = tonumber(redis.call('GET', KEYS[i]) or '0')
  if count >= tonumber(ARGV[i]) then return i end
end
for i = 1, #KEYS do
  if redis.call('INCR', KEYS[i]) == 1 then
    redis.call('PEXPIRE', KEYS[i], ARGV[#KEYS + 1])
  end
end
return 0
`

	emailAttemptScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local remaining = redis.call('PTTL', KEYS[1])
if remaining <= 0 then return 0 end
local count = tonumber(redis.call('GET', KEYS[2]) or '0')
if count >= tonumber(ARGV[2]) then return -1 end
if redis.call('INCR', KEYS[2]) == 1 then
  redis.call('PEXPIRE', KEYS[2], remaining)
end
return 1
`

	deleteEmailChallengeScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('DEL', unpack(KEYS))
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

type emailSendBucket struct {
	key     string
	limit   int
	message string
}

func checkSendEmailRateLimit(c fiber.Ctx, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, ownerID, email string) error {
	cache := helper.DBManager.Redis
	buckets := []emailSendBucket{
		{harukiRedis.BuildQQMailSendRateLimitUserKey(ownerID), emailSendUserLimit, "too many verification emails sent by this user"},
		{harukiRedis.BuildEmailVerifySendRateLimitIPKey(c.IP()), emailSendIPLimit, "too many requests from this IP"},
		{cache.KeyBuilder().BuildEmailVerifySendRateLimitTargetKey(email), emailSendTargetLimit, "too many verification emails sent to this address"},
	}
	keys := make([]string, 0, len(buckets))
	args := make([]any, 0, len(buckets)+1)
	for _, bucket := range buckets {
		keys = append(keys, bucket.key)
		args = append(args, bucket.limit)
	}
	args = append(args, emailSendRateLimitWindow.Milliseconds())
	rejected, err := cache.Redis.Eval(c.Context(), emailSendRateLimitReserveScript, keys, args...).Int()
	if err != nil {
		harukiLogger.Errorf("Failed to reserve email send rate limit: %v", err)
		return fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	if rejected == 0 {
		return nil
	}
	bucket := buckets[rejected-1]
	retryAfter := int64(emailSendRateLimitWindow.Seconds())
	if ttl, err := cache.Redis.TTL(c.Context(), bucket.key).Result(); err == nil && ttl > 0 {
		retryAfter = max(1, int64(ttl.Seconds()))
	}
	c.Set("Retry-After", fmt.Sprint(retryAfter))
	return fiber.NewError(fiber.StatusTooManyRequests, bucket.message)
}

func SendEmailHandler(c fiber.Ctx, ownerID, email, challengeToken string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, turnstileVerifier harukiCloudflare.Verifier) error {
	email = platformIdentity.NormalizeEmail(email)
	if email == "" || ownerID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email and owner are required")
	}
	ctx := c.Context()
	resp, err := harukiCloudflare.Verify(ctx, turnstileVerifier, challengeToken, c.IP())
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "captcha service unavailable")
	}
	if resp == nil || !resp.Success {
		return fiber.NewError(fiber.StatusBadRequest, "captcha verify failed")
	}
	if err := checkSendEmailRateLimit(c, helper, ownerID, email); err != nil {
		return err
	}
	code, err := GenerateCode(false)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to generate verification code")
	}
	msg, err := mailtemplate.Render(mailtemplate.EmailVerificationCode(code, emailCodeTTL))
	if err != nil || helper.SMTPClient == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	cache := helper.DBManager.Redis
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey(ownerID, email)
	challenge := uuid.NewString() + ":" + code
	if err := cache.Redis.Set(ctx, key, challenge, emailCodeTTL).Err(); err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	if err := helper.SMTPClient.SendMessage([]string{email}, msg, ""); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if cleanupErr := cache.Redis.Eval(cleanup, deleteEmailChallengeScript, []string{key}, challenge).Err(); cleanupErr != nil {
			harukiLogger.Warnf("Failed to discard an undelivered verification challenge: %v", cleanupErr)
		}
		return fiber.NewError(fiber.StatusServiceUnavailable, "failed to send verification email")
	}
	return nil
}

func VerifyEmailHandler(c fiber.Ctx, ownerID, email, oneTimePassword string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	ctx := c.Context()
	cache := helper.DBManager.Redis
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey(ownerID, email)
	challenge, err := cache.Redis.Get(ctx, key).Result()
	expired := fiber.NewError(fiber.StatusBadRequest, "verification code expired or not found")
	unavailable := fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	if errors.Is(err, goredis.Nil) {
		return expired
	}
	if err != nil {
		return unavailable
	}
	generation, code, ok := strings.Cut(challenge, ":")
	if !ok || generation == "" || len(code) != 6 {
		return expired
	}
	ipAttempts, err := cache.IncrementWithTTL(ctx, harukiRedis.BuildOTPVerifyAttemptIPKey(c.IP()), otpVerifyIPWindow)
	if err != nil {
		return unavailable
	}
	if ipAttempts > otpVerifyPerIPLimit {
		return fiber.NewError(fiber.StatusTooManyRequests, "too many verification attempts")
	}
	attemptKey := cache.KeyBuilder().BuildOwnedEmailAttemptKey(generation)
	keys := []string{key, attemptKey}
	attempt, err := cache.Redis.Eval(ctx, emailAttemptScript, keys, challenge, otpVerifyPerCodeLimit).Int()
	if err != nil {
		return unavailable
	}
	if attempt == 0 {
		return expired
	}
	if attempt < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "too many verification attempts; request a new code")
	}
	if subtle.ConstantTimeCompare([]byte(oneTimePassword), []byte(code)) != 1 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid verification code")
	}
	deleted, err := cache.Redis.Eval(ctx, deleteEmailChallengeScript, keys, challenge).Int()
	if err != nil {
		return unavailable
	}
	if deleted == 0 {
		return expired
	}
	return nil
}
