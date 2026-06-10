package userpasswordreset

import (
	userModule "haruki-suite/internal/modules/user"
	userauth "haruki-suite/internal/modules/userauth"
	userCoreModule "haruki-suite/internal/modules/usercore"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	harukiLogger "haruki-suite/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
)

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
