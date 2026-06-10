package userpasswordreset

import (
	userauth "haruki-suite/internal/modules/userauth"
	userCoreModule "haruki-suite/internal/modules/usercore"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	harukiLogger "haruki-suite/utils/logger"

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
