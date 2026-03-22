package userpasswordreset

import (
	"fmt"
	userModule "haruki-suite/internal/modules/user"
	userauth "haruki-suite/internal/modules/userauth"
	userCoreModule "haruki-suite/internal/modules/usercore"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"net/url"
	"strings"
	"time"

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

func handleSendResetPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
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
		if err != nil {
			reason = "challenge_service_unavailable"
			return harukiAPIHelper.ErrorInternal(c, "captcha service unavailable")
		}
		if resp == nil || !resp.Success {
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
			return respondResetPasswordRateLimitedWithWindow(c, limitKey, limitMessage, resetPasswordSendRateLimitWindow, apiHelper)
		}
		ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
		reservationCommitted := false
		redisKey := ""
		secretStored := false
		defer func() {
			if reservationCommitted {
				return
			}
			if secretStored && redisKey != "" {
				if delErr := apiHelper.DBManager.Redis.DeleteCache(ctx, redisKey); delErr != nil {
					harukiLogger.Warnf("Failed to rollback reset secret for %s: %v", payload.Email, delErr)
				}
			}
			if releaseErr := releaseResetPasswordSendRateLimitReservation(c, apiHelper, clientIP, payload.Email); releaseErr != nil {
				harukiLogger.Warnf("Failed to release reset-password rate limit reservation for %s: %v", payload.Email, releaseErr)
			}
		}()
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() {
			err := handleSendResetPasswordViaKratos(c, apiHelper, payload.Email, &result, &reason)
			if err == nil {
				reservationCommitted = true
			}
			return err
		}

		reason = "managed_identity_required"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusGone, userauth.ManagedIdentityMessage, nil)
	}
}

func handleSendResetPasswordViaKratos(
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	email string,
	result *string,
	reason *string,
) error {
	if err := apiHelper.SessionHandler.StartKratosRecoveryByEmail(c.Context(), email); err != nil {
		if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
			*reason = "identity_provider_unavailable"
			return harukiAPIHelper.ErrorInternal(c, "reset service unavailable")
		}
		harukiLogger.Errorf("Failed to start Kratos recovery flow for %s: %v", email, err)
		*reason = "start_kratos_recovery_failed"
		return harukiAPIHelper.ErrorInternal(c, "reset service unavailable")
	}
	*result = harukiAPIHelper.SystemLogResultSuccess
	*reason = "ok"
	return harukiAPIHelper.SuccessResponse[string](c, "Reset password email sent", nil)
}

func handleResetPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := ""
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		sessionClearFailed := false
		localMirrorFailed := false
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.reset_password.apply", result, targetUserID, map[string]any{
				"reason":             reason,
				"sessionClearFailed": sessionClearFailed,
				"localMirrorFailed":  localMirrorFailed,
			})
		}()

		var payload harukiAPIHelper.ResetPasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid payload")
		}
		payload.Email = platformIdentity.NormalizeEmail(payload.Email)
		if userModule.IsPasswordTooShort(payload.Password) {
			reason = "password_too_short"
			return harukiAPIHelper.ErrorBadRequest(c, userModule.PasswordTooShortMessage)
		}
		if userModule.IsPasswordTooLong(payload.Password) {
			reason = "password_too_long"
			return harukiAPIHelper.ErrorBadRequest(c, userModule.PasswordTooLongMessage)
		}
		rateLimitTarget := payload.Email
		if strings.TrimSpace(rateLimitTarget) == "" {
			rateLimitTarget = payload.OneTimeSecret
		}
		limited, limitKey, limitMessage, err := checkResetPasswordApplyRateLimit(c, apiHelper, c.IP(), rateLimitTarget)
		if err != nil {
			reason = "rate_limit_check_failed"
			return harukiAPIHelper.ErrorInternal(c, "reset service unavailable")
		}
		if limited {
			reason = "rate_limited"
			return respondResetPasswordRateLimited(c, limitKey, limitMessage, apiHelper)
		}
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() {
			return handleResetPasswordViaKratos(c, apiHelper, payload, &targetUserID, &result, &reason, &sessionClearFailed, &localMirrorFailed)
		}
		reason = "managed_identity_required"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusGone, userauth.ManagedIdentityMessage, nil)
	}
}
func handleResetPasswordViaKratos(
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	payload harukiAPIHelper.ResetPasswordPayload,
	targetUserID *string,
	result *string,
	reason *string,
	sessionClearFailed *bool,
	localMirrorFailed *bool,
) error {
	recoveryCode := strings.TrimSpace(payload.OneTimeSecret)
	if recoveryCode == "" {
		*reason = "invalid_reset_secret"
		return harukiAPIHelper.ErrorBadRequest(c, "Reset code expired or invalid")
	}
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	userID, identityID, err := apiHelper.SessionHandler.ResetKratosPasswordByRecoveryCode(ctx, recoveryCode, payload.Password)
	if err != nil {
		switch {
		case harukiAPIHelper.IsKratosInvalidCredentialsError(err), harukiAPIHelper.IsKratosInvalidInputError(err):
			*reason = "invalid_reset_secret"
			return harukiAPIHelper.ErrorBadRequest(c, "Reset code expired or invalid")
		case harukiAPIHelper.IsKratosIdentityUnmappedError(err):
			*reason = "identity_not_linked"
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		case harukiAPIHelper.IsIdentityProviderUnavailableError(err):
			*reason = "identity_provider_unavailable"
			return harukiAPIHelper.ErrorInternal(c, "Reset service unavailable")
		default:
			harukiLogger.Errorf("Failed to reset kratos password by recovery code: %v", err)
			*reason = "update_kratos_password_failed"
			return harukiAPIHelper.ErrorInternal(c, "Failed to update password")
		}
	}
	if strings.TrimSpace(userID) == "" {
		*reason = "invalid_user"
		return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
	}
	*targetUserID = userID

	_ = localMirrorFailed

	if strings.TrimSpace(identityID) != "" {
		if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(ctx, identityID); err != nil {
			harukiLogger.Warnf("Failed to revoke Kratos sessions for user %s: %v", userID, err)
			*sessionClearFailed = true
		}
	}
	if err := harukiAPIHelper.ClearUserSessions(apiHelper.RedisClient(), userID); err != nil {
		harukiLogger.Warnf("Failed to clear user sessions: %v", err)
		*sessionClearFailed = true
	}

	if *localMirrorFailed && *sessionClearFailed {
		*result = harukiAPIHelper.SystemLogResultSuccess
		*reason = "ok_local_mirror_and_session_clear_failed"
		return harukiAPIHelper.SuccessResponse[string](c, "Password reset successfully, but local mirror sync failed and some sessions could not be cleared", nil)
	}
	if *localMirrorFailed {
		*result = harukiAPIHelper.SystemLogResultSuccess
		*reason = "ok_local_mirror_failed"
		return harukiAPIHelper.SuccessResponse[string](c, "Password reset successfully, but local mirror sync failed", nil)
	}
	if *sessionClearFailed {
		*result = harukiAPIHelper.SystemLogResultSuccess
		*reason = "ok_session_clear_failed"
		return harukiAPIHelper.SuccessResponse[string](c, "Password reset successfully, but failed to clear existing sessions", nil)
	}
	*result = harukiAPIHelper.SystemLogResultSuccess
	*reason = "ok"
	return harukiAPIHelper.SuccessResponse[string](c, "Password reset successfully", nil)
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
	if apiHelper == nil || apiHelper.Router == nil {
		return
	}

	a := apiHelper.Router.Group("/api/user")
	if apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesManagedBrowserAuth() {
		disabled := userauth.LegacyAuthDisabledHandler()
		a.Post("/reset-password/send", disabled)
		a.Post("/reset-password", disabled)
		return
	}

	a.Post("/reset-password/send", handleSendResetPassword(apiHelper))
	a.Post("/reset-password", handleResetPassword(apiHelper))
}
