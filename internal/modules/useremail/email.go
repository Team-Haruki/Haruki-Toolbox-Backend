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
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

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

	// KEYS contains the counters, their window identities, then the receipt.
	emailSendRateLimitReserveScript = `
local receipt = KEYS[#KEYS]
if redis.call('EXISTS', receipt) == 1 then return 0 end
local buckets = (#KEYS - 1) / 2
for i = 1, buckets do
  local count = tonumber(redis.call('GET', KEYS[i]) or '0')
  if count >= tonumber(ARGV[i]) then return i end
end
local window = tonumber(ARGV[buckets + 1])
local identities = {}
for i = 1, buckets do
  local ttl = redis.call('PTTL', KEYS[i])
  local identity = redis.call('GET', KEYS[buckets + i])
  redis.call('INCR', KEYS[i])
  if ttl < 0 then
    -- Retain existing counters that were created without an expiry.
    redis.call('PEXPIRE', KEYS[i], window)
    ttl, identity = window, false
  end
  if not identity then
    identity = receipt
    redis.call('SET', KEYS[buckets + i], identity, 'PX', math.max(1, ttl))
  end
  identities[i] = identity
end
redis.call('SET', receipt, cjson.encode(identities), 'PX', window)
return 0
`

	emailSendRateLimitReleaseScript = `
local receipt = KEYS[#KEYS]
local raw = redis.call('GET', receipt)
if not raw then return 0 end
local identities = cjson.decode(raw)
for i = 1, #identities do
  if redis.call('GET', KEYS[#identities + i]) == identities[i] and
     tonumber(redis.call('GET', KEYS[i]) or '0') > 0 then
    redis.call('DECR', KEYS[i])
  end
end
return redis.call('DEL', receipt)
`

	// A resend temporarily replaces the active challenge. Keep its previous
	// value under the previous expiry until SMTP gives a result.
	stageEmailChallengeScript = `
local sending = redis.call('GET', KEYS[2])
if sending == ARGV[2] then return 1 end
local previous = redis.call('GET', KEYS[1])
if sending or (previous and string.sub(previous, 1, 8) == 'claimed:') then return 0 end
redis.call('DEL', KEYS[3])
local ttl = redis.call('PTTL', KEYS[1])
if previous and ttl > 0 then
  redis.call('SET', KEYS[3], previous, 'PX', ttl)
end
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[3])
return 1
`

	finishEmailChallengeScript = `
if redis.call('GET', KEYS[2]) ~= ARGV[2] then return 0 end
if ARGV[3] == 'restore' and redis.call('GET', KEYS[1]) == ARGV[1] then
  local previous = redis.call('GET', KEYS[3])
  local ttl = redis.call('PTTL', KEYS[3])
  if previous and ttl > 0 then
    redis.call('SET', KEYS[1], previous, 'PX', ttl)
  else
    redis.call('DEL', KEYS[1])
  end
end
return redis.call('DEL', KEYS[2], KEYS[3])
`

	// A valid code is reserved for the binding write. A rejected write may
	// restore it without extending the original challenge's expiry.
	emailAttemptScript = `
local current = redis.call('GET', KEYS[1])
if ARGV[3] ~= '' and current == ARGV[3] then return 1 end
if current ~= ARGV[1] then return 0 end
local remaining = redis.call('PTTL', KEYS[1])
if remaining <= 0 then return 0 end
local count = tonumber(redis.call('GET', KEYS[2]) or '0')
if count >= tonumber(ARGV[2]) then return -1 end
if ARGV[3] ~= '' then
  redis.call('SET', KEYS[1], ARGV[3], 'PX', remaining)
  return 1
end
if redis.call('INCR', KEYS[2]) == 1 then
  redis.call('PEXPIRE', KEYS[2], remaining)
end
return 1
`

	deleteEmailChallengeScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('DEL', unpack(KEYS))
`

	restoreEmailChallengeScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
local remaining = redis.call('PTTL', KEYS[1])
if remaining <= 0 then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', remaining)
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

type emailSendBucket struct {
	key     string
	limit   int
	message string
}

func reserveEmailSend(c fiber.Ctx, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, ownerID, email string) ([]string, error) {
	cache := helper.DBManager.Redis
	buckets := []emailSendBucket{
		{harukiRedis.BuildQQMailSendRateLimitUserKey(ownerID), emailSendUserLimit, "too many verification emails sent by this user"},
		{harukiRedis.BuildEmailVerifySendRateLimitIPKey(c.IP()), emailSendIPLimit, "too many requests from this IP"},
		{cache.KeyBuilder().BuildEmailVerifySendRateLimitTargetKey(email), emailSendTargetLimit, "too many verification emails sent to this address"},
	}
	keys := make([]string, 0, 2*len(buckets)+1)
	args := make([]any, 0, len(buckets)+1)
	for _, bucket := range buckets {
		keys = append(keys, bucket.key)
		args = append(args, bucket.limit)
	}
	for _, bucket := range buckets {
		keys = append(keys, bucket.key+":window")
	}
	keys = append(keys, harukiRedis.BuildEmailVerifySendReservationKey(uuid.NewString()))
	args = append(args, emailSendRateLimitWindow.Milliseconds())
	rejected, err := cache.Redis.Eval(c.Context(), emailSendRateLimitReserveScript, keys, args...).Int()
	if err != nil {
		harukiLogger.Errorf("Failed to reserve email send rate limit: %v", err)
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	if rejected == 0 {
		return keys, nil
	}
	bucket := buckets[rejected-1]
	retryAfter := int64(emailSendRateLimitWindow.Seconds())
	if ttl, err := cache.Redis.TTL(c.Context(), bucket.key).Result(); err == nil && ttl > 0 {
		retryAfter = max(1, int64(ttl.Seconds()))
	}
	c.Set("Retry-After", fmt.Sprint(retryAfter))
	return nil, fiber.NewError(fiber.StatusTooManyRequests, bucket.message)
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
	if helper.SMTPClient == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	reservation, err := reserveEmailSend(c, helper, ownerID, email)
	if err != nil {
		return err
	}
	cache := helper.DBManager.Redis
	refund := true
	defer func() {
		if refund {
			if err := reconcileEmailState(ctx, cache.Redis, emailSendRateLimitReleaseScript, reservation); err != nil {
				harukiLogger.Warnf("Failed to release email send reservation: %v", err)
			}
		}
	}()
	code, err := GenerateCode(false)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to generate verification code")
	}
	msg, err := mailtemplate.Render(mailtemplate.EmailVerificationCode(code, emailCodeTTL))
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey(ownerID, email)
	generation := uuid.NewString()
	challenge := generation + ":" + code
	keys := []string{key, key + ":sending", key + ":previous"}
	staged, err := cache.Redis.Eval(ctx, stageEmailChallengeScript, keys, challenge, generation, emailCodeTTL.Milliseconds()).Int()
	if err != nil {
		if cleanupErr := reconcileEmailState(ctx, cache.Redis, finishEmailChallengeScript, keys, challenge, generation, "restore"); cleanupErr != nil {
			harukiLogger.Warnf("Failed to roll back verification email preparation: %v", cleanupErr)
		}
		return fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	if staged == 0 {
		return fiber.NewError(fiber.StatusServiceUnavailable, "verification service unavailable")
	}
	sendErr := helper.SMTPClient.SendMessage([]string{email}, msg, "")
	refund = errors.Is(sendErr, harukiSMTP.ErrMessageNotAccepted)
	mode := "keep"
	if refund {
		mode = "restore"
	}
	if err := reconcileEmailState(ctx, cache.Redis, finishEmailChallengeScript, keys, challenge, generation, mode); err != nil {
		harukiLogger.Warnf("Failed to reconcile the verification email result: %v", err)
	}
	if sendErr != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "failed to send verification email")
	}
	return nil
}

// The cleanup scripts are safe to repeat after a missing Redis reply.
func reconcileEmailState(ctx context.Context, client *goredis.Client, script string, keys []string, args ...any) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-time.After(20 * time.Millisecond):
			case <-cleanup.Done():
				return errors.Join(err, cleanup.Err())
			}
		}
		err = client.Eval(cleanup, script, keys, args...).Err()
		if err == nil {
			return nil
		}
	}
	return err
}

// VerifyEmailHandler reserves the challenge before running complete and
// consumes it after that operation succeeds. A failure leaves a current
// challenge available for retry.
func VerifyEmailHandler(c fiber.Ctx, ownerID, email, oneTimePassword string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, complete func() error) error {
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
	reservation := ""
	if subtle.ConstantTimeCompare([]byte(oneTimePassword), []byte(code)) == 1 {
		reservation = "claimed:" + uuid.NewString()
	}
	attempt, err := cache.Redis.Eval(ctx, emailAttemptScript, keys, challenge, otpVerifyPerCodeLimit, reservation).Int()
	if err != nil {
		if reservation != "" {
			if err := reconcileEmailState(ctx, cache.Redis, restoreEmailChallengeScript, []string{key}, reservation, challenge); err != nil {
				harukiLogger.Warnf("Failed to roll back verification challenge reservation: %v", err)
			}
		}
		return unavailable
	}
	if attempt == 0 {
		return expired
	}
	if attempt < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "too many verification attempts; request a new code")
	}
	if reservation == "" {
		return fiber.NewError(fiber.StatusBadRequest, "invalid verification code")
	}

	// The claimed challenge cannot be used again while the binding is being
	// written, even if the request is cancelled or the final cleanup fails.
	actionErr := complete()
	if actionErr != nil {
		if err := reconcileEmailState(ctx, cache.Redis, restoreEmailChallengeScript, []string{key}, reservation, challenge); err != nil {
			harukiLogger.Warnf("Failed to restore an unused verification challenge: %v", err)
		}
		return actionErr
	}
	if err := reconcileEmailState(ctx, cache.Redis, deleteEmailChallengeScript, keys, reservation); err != nil {
		harukiLogger.Warnf("Failed to clean up a completed verification challenge: %v", err)
	}
	return nil
}
