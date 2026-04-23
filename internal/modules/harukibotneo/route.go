package harukibotneo

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/neopg"
	botUser "haruki-suite/utils/database/neopg/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/smtp"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	verifyCodeTTL     = 10 * time.Minute
	verifyCodeLen     = 6
	maxVerifyAttempts = 5

	sendMailRateLimitWindow = 60 * time.Minute
	sendMailIPLimit         = 20
	sendMailTargetLimit     = 5

	registerRateLimitWindow = 10 * time.Minute
	registerTargetLimit     = 5

	botIDMin     = 10000000
	botIDMax     = 99999999
	botIDRetries = 10

	credentialBytes = 32

	rateLimitLimitedByNone   = int64(0)
	rateLimitLimitedByIP     = int64(1)
	rateLimitLimitedByTarget = int64(2)

	sendMailRateLimitScript = `
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

	sendMailRateLimitReleaseScript = `
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

type sendMailPayload struct {
	QQNumber int64 `json:"qq_number"`
}

type registerPayload struct {
	QQNumber         int64  `json:"qq_number"`
	VerificationCode string `json:"verification_code"`
}

type registrationStatusResponse struct {
	Enabled bool `json:"enabled"`
}

type registrationResultData struct {
	BotID      string `json:"bot_id"`
	Credential string `json:"credential"`
}

func RegisterHarukiBotNeoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	botAPI := apiHelper.Router.Group("/api/haruki-bot-neo")

	botAPI.Get("/status", handleGetStatus(apiHelper))

	botAPI.Post("/send-mail",
		apiHelper.SessionHandler.VerifySessionToken,
		handleSendMail(apiHelper),
	)
	botAPI.Post("/register",
		apiHelper.SessionHandler.VerifySessionToken,
		handleRegister(apiHelper),
	)
}

func handleGetStatus(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		resp := registrationStatusResponse{Enabled: apiHelper.BotRegistrationEnabled}
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func handleSendMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !apiHelper.BotRegistrationEnabled {
			return harukiAPIHelper.ErrorForbidden(c, "registration is currently disabled")
		}
		if apiHelper.DBManager.BotDB == nil {
			harukiLogger.Errorf("bot database is not configured")
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}

		var payload sendMailPayload
		if err := c.Bind().JSON(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		if payload.QQNumber <= 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "missing qq_number")
		}

		qqStr := strconv.FormatInt(payload.QQNumber, 10)
		ctx := c.Context()

		// Rate limit
		clientIP := c.IP()
		limited, limitKey, limitMsg, rlErr := checkSendMailRateLimit(c, apiHelper, clientIP, qqStr)
		if rlErr != nil {
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}
		if limited {
			return respondRateLimited(c, limitKey, limitMsg, apiHelper, sendMailRateLimitWindow)
		}

		// Generate and store code
		code, err := generateCode()
		if err != nil {
			harukiLogger.Errorf("Failed to generate verification code: %v", err)
			releaseSendMailRateLimit(c, apiHelper, clientIP, qqStr)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate verification code")
		}
		redisKey := harukiRedis.BuildBotVerifyCodeKey(qqStr)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, redisKey, code, verifyCodeTTL); err != nil {
			harukiLogger.Errorf("Failed to store verification code: %v", err)
			releaseSendMailRateLimit(c, apiHelper, clientIP, qqStr)
			return harukiAPIHelper.ErrorInternal(c, "failed to save verification code")
		}

		// Send email
		email := fmt.Sprintf("%s@qq.com", qqStr)
		body := strings.ReplaceAll(smtp.VerificationCodeTemplate, "{{CODE}}", code)
		if err := apiHelper.SMTPClient.Send([]string{email}, "您的验证码 | Haruki Bot", body, "Haruki Bot | 星云科技"); err != nil {
			if delErr := apiHelper.DBManager.Redis.DeleteCache(ctx, redisKey); delErr != nil {
				harukiLogger.Warnf("Failed to rollback verification code for QQ %s: %v", qqStr, delErr)
			}
			releaseSendMailRateLimit(c, apiHelper, clientIP, qqStr)
			harukiLogger.Errorf("Failed to send email to %s: %v", email, err)
			return harukiAPIHelper.ErrorInternal(c, "failed to send verification email")
		}

		return harukiAPIHelper.SuccessResponse[string](c, "verification code sent", nil)
	}
}

func handleRegister(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if !apiHelper.BotRegistrationEnabled {
			return harukiAPIHelper.ErrorForbidden(c, "registration is currently disabled")
		}
		if apiHelper.DBManager.BotDB == nil {
			harukiLogger.Errorf("bot database is not configured")
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}

		var payload registerPayload
		if err := c.Bind().JSON(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		if payload.QQNumber <= 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "missing qq_number")
		}
		if strings.TrimSpace(payload.VerificationCode) == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing verification_code")
		}

		qqStr := strconv.FormatInt(payload.QQNumber, 10)
		ctx := c.Context()

		// Register rate limit
		rlKey := harukiRedis.BuildBotRegisterRateLimitTargetKey(qqStr)
		count, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, rlKey, registerRateLimitWindow)
		if err != nil {
			harukiLogger.Errorf("Failed to check register rate limit: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}
		if count > int64(registerTargetLimit) {
			return respondRateLimited(c, rlKey, "too many registration attempts", apiHelper, registerRateLimitWindow)
		}

		// Verify code
		codeKey := harukiRedis.BuildBotVerifyCodeKey(qqStr)
		var storedCode string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, codeKey, &storedCode)
		if err != nil {
			harukiLogger.Errorf("Failed to get verification code: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}
		if !found {
			return harukiAPIHelper.ErrorBadRequest(c, "verification code not found or expired")
		}
		if subtle.ConstantTimeCompare([]byte(payload.VerificationCode), []byte(storedCode)) != 1 {
			// Track attempts
			attemptKey := harukiRedis.BuildBotVerifyAttemptKey(qqStr)
			var attemptCount int
			if af, _ := apiHelper.DBManager.Redis.GetCache(ctx, attemptKey, &attemptCount); af && attemptCount >= maxVerifyAttempts {
				_ = apiHelper.DBManager.Redis.DeleteCache(ctx, codeKey)
				_ = apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey)
				return harukiAPIHelper.ErrorBadRequest(c, "too many verification attempts, please request a new code")
			}
			_ = apiHelper.DBManager.Redis.SetCache(ctx, attemptKey, attemptCount+1, verifyCodeTTL)
			return harukiAPIHelper.ErrorBadRequest(c, "verification code is invalid")
		}

		// Consume code
		consumed, err := apiHelper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, codeKey, storedCode)
		if err != nil || !consumed {
			return harukiAPIHelper.ErrorBadRequest(c, "verification code not found or expired")
		}

		// Generate new credential
		credentialRaw := make([]byte, credentialBytes)
		if _, err := rand.Read(credentialRaw); err != nil {
			harukiLogger.Errorf("Failed to generate credential: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate credential")
		}
		credentialPlain := base64.URLEncoding.EncodeToString(credentialRaw)

		hashedCredential, err := bcrypt.GenerateFromPassword([]byte(credentialPlain), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash credential: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to process credential")
		}

		// Check if already registered — update credential if so, otherwise create new
		existing, err := apiHelper.DBManager.BotDB.User.Query().
			Where(botUser.OwnerUserIDEQ(payload.QQNumber)).
			Only(ctx)
		if err != nil && !neopg.IsNotFound(err) {
			harukiLogger.Errorf("Failed to check bot registration: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "registration service unavailable")
		}

		var botIDStr string
		var statusCode int
		var message string

		if existing != nil {
			// Update existing credential
			_, err = existing.Update().
				SetCredential(string(hashedCredential)).
				Save(ctx)
			if err != nil {
				harukiLogger.Errorf("Failed to update bot credential: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to update credential")
			}
			botIDStr = strconv.Itoa(existing.BotID)
			statusCode = fiber.StatusOK
			message = "credential reset successful"
		} else {
			// Generate bot_id for new registration
			botID, err := generateUniqueBotID(ctx, apiHelper.DBManager.BotDB)
			if err != nil {
				harukiLogger.Errorf("Failed to generate bot_id: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to generate bot ID")
			}
			_, err = apiHelper.DBManager.BotDB.User.Create().
				SetOwnerUserID(payload.QQNumber).
				SetBotID(botID).
				SetCredential(string(hashedCredential)).
				Save(ctx)
			if err != nil {
				harukiLogger.Errorf("Failed to create bot registration: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to create registration")
			}
			botIDStr = strconv.Itoa(botID)
			statusCode = fiber.StatusCreated
			message = "registration successful"
		}

		// Sign credential as JWT
		credentialJWT, err := signCredentialJWT(apiHelper.BotCredentialSignToken, botIDStr, credentialPlain)
		if err != nil {
			harukiLogger.Errorf("Failed to sign credential JWT: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to sign credential")
		}

		// Cleanup Redis
		attemptKey := harukiRedis.BuildBotVerifyAttemptKey(qqStr)
		_ = apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey)

		result := registrationResultData{
			BotID:      botIDStr,
			Credential: credentialJWT,
		}
		return harukiAPIHelper.UpdatedDataResponse(c, statusCode, message, &result)
	}
}

func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func generateUniqueBotID(ctx context.Context, botDB *neopg.Client) (int, error) {
	for range botIDRetries {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(botIDMax-botIDMin+1)))
		if err != nil {
			return 0, err
		}
		botID := int(n.Int64()) + botIDMin
		exists, err := botDB.User.Query().
			Where(botUser.BotIDEQ(botID)).
			Exist(ctx)
		if err != nil {
			return 0, err
		}
		if !exists {
			return botID, nil
		}
	}
	return 0, fmt.Errorf("failed to generate unique bot_id after %d attempts", botIDRetries)
}

func signCredentialJWT(secret, botID, credential string) (string, error) {
	claims := jwt.MapClaims{
		"bot_id":     botID,
		"credential": credential,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

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
