package useremail

import (
	"crypto/rand"
	"fmt"
	userCoreModule "haruki-suite/internal/modules/usercore"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
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
	ctx := c.Context()
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

func SendEmailHandler(c fiber.Ctx, email, challengeToken string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) error {
	ctx := c.Context()
	email = platformIdentity.NormalizeEmail(email)
	if email == "" {
		return harukiAPIHelper.ErrorBadRequest(c, "email is required")
	}
	clientIP := c.IP()
	resp, err := cloudflare.ValidateTurnstile(challengeToken, clientIP)
	if err != nil || resp == nil || !resp.Success {
		return harukiAPIHelper.ErrorBadRequest(c, "captcha verify failed")
	}
	limited, limitKey, limitMessage, err := checkSendEmailRateLimit(c, helper, clientIP, email)
	if err != nil {
		return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
	}
	if limited {
		return respondEmailSendRateLimited(c, limitKey, limitMessage, helper)
	}
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
		harukiLogger.Errorf("Failed to send email: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to send email")
	}
	return harukiAPIHelper.SuccessResponse[string](c, "verification code sent", nil)
}

func VerifyEmailHandler(c fiber.Ctx, email, oneTimePassword string, helper *harukiAPIHelper.HarukiToolboxRouterHelpers) (bool, error) {
	ctx := c.Context()
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

func resolveVerifyEmailFinalizeOutcome(localMirrorFailed, sessionClearFailed bool) (status int, message string, result string, reason string) {
	if localMirrorFailed && sessionClearFailed {
		return fiber.StatusInternalServerError, "email verified in identity provider, but local mirror sync failed and some sessions were not cleared", harukiAPIHelper.SystemLogResultFailure, "local_mirror_and_session_clear_failed"
	}
	if localMirrorFailed {
		return fiber.StatusInternalServerError, "email verified in identity provider, but local mirror sync failed", harukiAPIHelper.SystemLogResultFailure, "local_mirror_failed"
	}
	if sessionClearFailed {
		return fiber.StatusOK, "email verified, but failed to clear existing sessions", harukiAPIHelper.SystemLogResultSuccess, "ok_session_clear_failed"
	}
	return fiber.StatusOK, "email verified", harukiAPIHelper.SystemLogResultSuccess, "ok"
}

func handleSendEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var req harukiAPIHelper.SendEmailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		req.Email = platformIdentity.NormalizeEmail(req.Email)
		if req.Email == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "email is required")
		}
		exists, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.EmailEqualFold(req.Email)).Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query user email: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query database")
		}
		if exists {
			return harukiAPIHelper.ErrorBadRequest(c, "email already exists")
		}
		return SendEmailHandler(c, req.Email, req.ChallengeToken, apiHelper)
	}
}

func handleVerifyEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		sessionClearFailed := false
		localMirrorFailed := false
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.email.verify", result, userID, map[string]any{
				"reason":             reason,
				"sessionClearFailed": sessionClearFailed,
				"localMirrorFailed":  localMirrorFailed,
			})
		}()

		var req harukiAPIHelper.VerifyEmailPayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		req.Email = platformIdentity.NormalizeEmail(req.Email)
		if req.Email == "" {
			reason = "invalid_email"
			return harukiAPIHelper.ErrorBadRequest(c, "email is required")
		}
		ok, err := VerifyEmailHandler(c, req.Email, req.OneTimePassword, apiHelper)
		if err != nil {
			reason = "verify_email_otp_failed"
			return err
		}
		if !ok {
			reason = "verify_email_otp_failed"
			return harukiAPIHelper.ErrorBadRequest(c, "verification failed")
		}

		emailTaken, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.EmailEqualFold(req.Email), userSchema.IDNEQ(userID)).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to check email availability: %v", err)
			reason = "check_email_taken_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to check email availability")
		}
		if emailTaken {
			reason = "email_taken"
			return harukiAPIHelper.ErrorBadRequest(c, "email already in use by another account")
		}

		currentUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Select(userSchema.FieldID, userSchema.FieldKratosIdentityID).
			Only(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query current user for email verify: %v", err)
			reason = "query_user_failed"
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query user")
		}

		kratosIdentityID := ""
		if currentUser.KratosIdentityID != nil {
			kratosIdentityID = strings.TrimSpace(*currentUser.KratosIdentityID)
		}
		kratosUpdated := false
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() && kratosIdentityID != "" {
			if err := apiHelper.SessionHandler.UpdateKratosEmailByIdentityID(ctx, kratosIdentityID, req.Email); err != nil {
				switch {
				case harukiAPIHelper.IsKratosIdentityConflictError(err):
					reason = "email_taken"
					return harukiAPIHelper.ErrorBadRequest(c, "email already in use by another account")
				case harukiAPIHelper.IsKratosIdentityUnmappedError(err):
					reason = "kratos_identity_unmapped"
					return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
				case harukiAPIHelper.IsIdentityProviderUnavailableError(err):
					harukiLogger.Errorf("Failed to sync Kratos email for user %s: %v", userID, err)
					reason = "identity_provider_unavailable"
					return harukiAPIHelper.ErrorInternal(c, "identity provider unavailable")
				default:
					harukiLogger.Errorf("Failed to update Kratos email for user %s: %v", userID, err)
					reason = "update_kratos_email_failed"
					return harukiAPIHelper.ErrorInternal(c, "failed to update user email")
				}
			}
			kratosUpdated = true
		}

		if _, err := apiHelper.DBManager.DB.User.
			Update().
			Where(userSchema.IDEQ(userID)).
			SetEmail(req.Email).
			Save(ctx); err != nil {
			if kratosUpdated {
				harukiLogger.Warnf("Kratos email updated but failed to sync local mirror for user %s: %v", userID, err)
				localMirrorFailed = true
			} else {
				harukiLogger.Errorf("Failed to update user email: %v", err)
				reason = "update_user_email_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to update user email")
			}
		}

		ud := harukiAPIHelper.HarukiToolboxUserData{
			EmailInfo: &harukiAPIHelper.EmailInfo{
				Email:    req.Email,
				Verified: true,
			},
		}
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() && kratosIdentityID != "" {
			if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(ctx, kratosIdentityID); err != nil {
				harukiLogger.Warnf("Failed to revoke Kratos sessions after email update for user %s: %v", userID, err)
				sessionClearFailed = true
			}
		}
		if err := harukiAPIHelper.ClearUserSessions(apiHelper.RedisClient(), userID); err != nil {
			harukiLogger.Warnf("Failed to clear local user sessions after email update: %v", err)
			sessionClearFailed = true
		}
		status, message, finalResult, finalReason := resolveVerifyEmailFinalizeOutcome(localMirrorFailed, sessionClearFailed)
		result = finalResult
		reason = finalReason
		if status == fiber.StatusOK {
			return harukiAPIHelper.SuccessResponse(c, message, &ud)
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, status, message, nil)
	}
}

func RegisterUserEmailRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	email := apiHelper.Router.Group("/api/email")

	email.Post("/send", handleSendEmail(apiHelper))
	email.Post("/verify", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleVerifyEmail(apiHelper))
}
