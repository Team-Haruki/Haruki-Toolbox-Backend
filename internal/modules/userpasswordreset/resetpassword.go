package userpasswordreset

import (
	"fmt"
	"haruki-suite/config"
	userModule "haruki-suite/internal/modules/user"
	userCoreModule "haruki-suite/internal/modules/usercore"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	resetPasswordSendRateLimitWindow = 10 * time.Minute
	resetPasswordSendIPLimit         = 20
	resetPasswordSendTargetLimit     = 3

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
)

func respondResetPasswordRateLimited(c fiber.Ctx, key string, message string, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	retryAfter := int64(resetPasswordSendRateLimitWindow.Seconds())
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
	ctx := c.Context()
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

func handleSendResetPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.reset_password.send", result, "", map[string]any{
				"reason": reason,
			})
		}()

		var payload harukiAPIHelper.SendResetPasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid payload")
		}
		payload.Email = platformIdentity.NormalizeEmail(payload.Email)
		if payload.Email == "" {
			reason = "invalid_email"
			return harukiAPIHelper.ErrorBadRequest(c, "email is required")
		}
		clientIP := c.IP()
		resp, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, clientIP)
		if err != nil || !resp.Success {
			reason = "invalid_challenge"
			return harukiAPIHelper.ErrorBadRequest(c, "captcha verify failed")
		}
		limited, limitKey, limitMessage, err := checkResetPasswordSendRateLimit(c, apiHelper, clientIP, payload.Email)
		if err != nil {
			reason = "rate_limit_check_failed"
			return harukiAPIHelper.ErrorInternal(c, "reset service unavailable")
		}
		if limited {
			reason = "rate_limited"
			return respondResetPasswordRateLimited(c, limitKey, limitMessage, apiHelper)
		}

		exists, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.EmailEqualFold(payload.Email)).Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query user: %v", err)
			reason = "query_user_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query database")
		}
		if !exists {
			result = harukiAPIHelper.SystemLogResultSuccess
			reason = "ok"
			return harukiAPIHelper.SuccessResponse[string](c, "Reset password email sent", nil)
		}
		resetSecret := uuid.NewString()
		resetURL := buildResetPasswordURL(config.Cfg.UserSystem.FrontendURL, resetSecret, payload.Email)
		redisKey := harukiRedis.BuildResetPasswordKey(payload.Email)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, redisKey, resetSecret, 30*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "save_reset_secret_failed"
			return harukiAPIHelper.ErrorInternal(c, "Failed to store secret")
		}
		body := strings.ReplaceAll(smtp.ResetPasswordTemplate, "{{LINK}}", resetURL)
		if err := apiHelper.SMTPClient.Send([]string{payload.Email}, "您的重设密码请求 | Haruki工具箱", body, "Haruki工具箱 | 星云科技"); err != nil {
			harukiLogger.Errorf("Failed to send email: %v", err)
			reason = "send_email_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to send email")
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "Reset password email sent", nil)
	}
}

func handleResetPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		targetUserID := ""
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		sessionClearFailed := false
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.reset_password.apply", result, targetUserID, map[string]any{
				"reason":             reason,
				"sessionClearFailed": sessionClearFailed,
			})
		}()

		var payload harukiAPIHelper.ResetPasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid payload")
		}
		payload.Email = platformIdentity.NormalizeEmail(payload.Email)
		if payload.Email == "" {
			reason = "invalid_email"
			return harukiAPIHelper.ErrorBadRequest(c, "email is required")
		}
		redisKey := harukiRedis.BuildResetPasswordKey(payload.Email)
		var secret string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, redisKey, &secret)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			reason = "get_reset_secret_failed"
			return harukiAPIHelper.ErrorInternal(c, "Failed to retrieve secret")
		}
		if !found {
			reason = "reset_secret_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "Reset code expired or invalid")
		}
		if secret != payload.OneTimeSecret {
			reason = "invalid_reset_secret"
			return harukiAPIHelper.ErrorBadRequest(c, "Incorrect reset code")
		}
		consumed, err := apiHelper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, redisKey, secret)
		if err != nil {
			harukiLogger.Errorf("Failed to consume reset secret: %v", err)
			reason = "consume_reset_secret_failed"
			return harukiAPIHelper.ErrorInternal(c, "Reset service unavailable")
		}
		if !consumed {
			reason = "reset_secret_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "Reset code expired or invalid")
		}
		u, err := apiHelper.DBManager.DB.User.
			Query().
			Where(userSchema.EmailEqualFold(payload.Email)).
			Only(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query user: %v", err)
			reason = "query_user_failed"
			return harukiAPIHelper.ErrorInternal(c, "Failed to locate user")
		}
		targetUserID = u.ID
		if userModule.IsPasswordTooShort(payload.Password) {
			reason = "password_too_short"
			return harukiAPIHelper.ErrorBadRequest(c, userModule.PasswordTooShortMessage)
		}
		if userModule.IsPasswordTooLong(payload.Password) {
			reason = "password_too_long"
			return harukiAPIHelper.ErrorBadRequest(c, userModule.PasswordTooLongMessage)
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash password: %v", err)
			reason = "hash_password_failed"
			return harukiAPIHelper.ErrorInternal(c, "Failed to hash password")
		}
		_, err = apiHelper.DBManager.DB.User.
			UpdateOneID(u.ID).
			SetPasswordHash(string(hashed)).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update user password: %v", err)
			reason = "update_password_failed"
			return harukiAPIHelper.ErrorInternal(c, "Failed to update password")
		}
		if err := harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, u.ID); err != nil {
			harukiLogger.Warnf("Failed to clear user sessions: %v", err)
			sessionClearFailed = true
			result = harukiAPIHelper.SystemLogResultSuccess
			reason = "ok_session_clear_failed"
			return harukiAPIHelper.SuccessResponse[string](c, "Password reset successfully, but failed to clear existing sessions", nil)
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "Password reset successfully", nil)
	}
}

func buildResetPasswordURL(frontendURL, resetSecret, email string) string {
	return fmt.Sprintf(
		"%s/user/reset-password/%s?email=%s",
		strings.TrimRight(frontendURL, "/"),
		resetSecret,
		url.QueryEscape(strings.TrimSpace(email)),
	)
}

func RegisterUserResetPasswordRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	a := apiHelper.Router.Group("/api/user")

	a.Post("/reset-password/send", handleSendResetPassword(apiHelper))
	a.Post("/reset-password", handleResetPassword(apiHelper))
}
