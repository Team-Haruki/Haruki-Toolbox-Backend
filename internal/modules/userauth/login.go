package userauth

import (
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

func handleLogin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		logLogin := func(result string, targetUserID string, actorRole string, reason string) {
			targetType := "user"
			var targetIDPtr *string
			if targetUserID != "" {
				targetID := targetUserID
				targetIDPtr = &targetID
			}
			entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, "user.login", result, &targetType, targetIDPtr, map[string]any{
				"reason": reason,
			})
			if targetUserID != "" {
				entry.ActorUserID = &targetUserID
				roleLower := normalizeAuditRole(actorRole)
				entry.ActorRole = &roleLower
				if isAdminAuditRole(roleLower) {
					entry.ActorType = harukiAPIHelper.SystemLogActorTypeAdmin
				} else {
					entry.ActorType = harukiAPIHelper.SystemLogActorTypeUser
				}
			}
			_ = harukiAPIHelper.WriteSystemLog(ctx, apiHelper, entry)
		}

		var payload harukiAPIHelper.LoginPayload
		if err := c.Bind().Body(&payload); err != nil {
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_payload")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid request")
		}
		payload.Email = platformIdentity.NormalizeEmail(payload.Email)
		if payload.Email == "" {
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_email")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
		}
		result, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, c.IP())
		if err != nil || result == nil || !result.Success {
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_challenge")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid Turnstile challenge")
		}
		limited, rateLimitKey, rateLimitMessage, err := checkLoginRateLimit(c, apiHelper, c.IP(), payload.Email)
		if err != nil {
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "rate_limit_check_failed")
			return harukiAPIHelper.ErrorInternal(c, "login service unavailable")
		}
		if limited {
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "rate_limited")
			return respondLoginRateLimited(c, rateLimitKey, rateLimitMessage, apiHelper)
		}
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() {
			return handleLoginViaKratos(c, apiHelper, payload, logLogin)
		}
		user, err := apiHelper.DBManager.DB.User.
			Query().
			Where(userSchema.EmailEqualFold(payload.Email)).
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			WithIosScriptCode().
			Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				harukiLogger.Infof("Login failed for email %s: user not found", payload.Email)
				logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_credentials")
				return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
			}
			rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
			harukiLogger.Errorf("Login failed for email %s: query error: %v", payload.Email, err)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "query_user_failed")
			return harukiAPIHelper.ErrorInternal(c, "login service unavailable")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
			harukiLogger.Infof("Login failed for email %s: invalid password", payload.Email)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_credentials")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
		}
		if user.Banned {
			banMessage := "Your account has been banned"
			if user.BanReason != nil && *user.BanReason != "" {
				banMessage = "Your account has been banned: " + *user.BanReason
			}
			logLogin(harukiAPIHelper.SystemLogResultFailure, user.ID, string(user.Role), "banned")
			return harukiAPIHelper.ErrorForbidden(c, banMessage)
		}
		sessionToken, err := apiHelper.SessionHandler.IssueSession(user.ID)
		if err != nil {
			rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
			harukiLogger.Errorf("Failed to issue session for user %s: %v", user.ID, err)
			logLogin(harukiAPIHelper.SystemLogResultFailure, user.ID, string(user.Role), "issue_session_failed")
			return harukiAPIHelper.ErrorInternal(c, "Could not issue session")
		}
		finalizeLoginRateLimitReservation(c, apiHelper, payload.Email)
		logLogin(harukiAPIHelper.SystemLogResultSuccess, user.ID, string(user.Role), "ok")
		ud := harukiAPIHelper.BuildUserDataFromDBUser(user, &sessionToken)
		resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "login success", UserData: ud}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
	}
}

func normalizeAuditRole(actorRole string) string {
	roleLower := strings.ToLower(strings.TrimSpace(actorRole))
	if roleLower == "" {
		return "user"
	}
	return roleLower
}

func isAdminAuditRole(role string) bool {
	return role == "admin" || role == "super_admin"
}

func rollbackLoginRateLimitReservation(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email string) {
	if rollbackErr := releaseLoginRateLimitReservation(c, apiHelper, c.IP(), email); rollbackErr != nil {
		harukiLogger.Warnf("Failed to rollback login rate limit reservation for email %s: %v", email, rollbackErr)
	}
}

func finalizeLoginRateLimitReservation(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, email string) {
	if err := releaseLoginRateLimitReservation(c, apiHelper, c.IP(), email); err != nil {
		harukiLogger.Warnf("Failed to release login rate limit reservation for email %s: %v", email, err)
	}
}

func handleLoginViaKratos(
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	payload harukiAPIHelper.LoginPayload,
	logLogin func(result string, targetUserID string, actorRole string, reason string),
) error {
	ctx := c.Context()

	sessionToken, err := apiHelper.SessionHandler.LoginWithKratosPassword(ctx, payload.Email, payload.Password)
	if err != nil {
		if harukiAPIHelper.IsKratosInvalidCredentialsError(err) {
			harukiLogger.Infof("Kratos login failed for email %s: invalid credentials", payload.Email)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_credentials")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
		}
		if harukiAPIHelper.IsKratosInvalidInputError(err) {
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_input")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
		}
		if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
			rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "identity_provider_unavailable")
			return harukiAPIHelper.ErrorInternal(c, "login service unavailable")
		}
		rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
		logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "kratos_login_failed")
		return harukiAPIHelper.ErrorInternal(c, "login service unavailable")
	}
	revokeIssuedSession := func(stage string) {
		if revokeErr := apiHelper.SessionHandler.RevokeKratosSessionByToken(ctx, sessionToken); revokeErr != nil {
			harukiLogger.Warnf("Failed to revoke issued Kratos session after %s for email %s: %v", stage, payload.Email, revokeErr)
		}
	}

	userID, err := apiHelper.SessionHandler.ResolveUserIDFromKratosSession(ctx, sessionToken, "")
	if err != nil {
		revokeIssuedSession("resolve user")
		if harukiAPIHelper.IsKratosIdentityUnmappedError(err) {
			rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "identity_unmapped")
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}
		if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
			rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "identity_provider_unavailable")
			return harukiAPIHelper.ErrorInternal(c, "login service unavailable")
		}
		rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
		logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "resolve_identity_failed")
		return harukiAPIHelper.ErrorInternal(c, "login service unavailable")
	}

	user, err := apiHelper.DBManager.DB.User.
		Query().
		Where(userSchema.IDEQ(userID)).
		WithSocialPlatformInfo().
		WithAuthorizedSocialPlatforms().
		WithGameAccountBindings().
		WithIosScriptCode().
		Only(ctx)
	if err != nil {
		revokeIssuedSession("load local user")
		if postgresql.IsNotFound(err) {
			rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "local_user_not_found")
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}
		rollbackLoginRateLimitReservation(c, apiHelper, payload.Email)
		harukiLogger.Errorf("Kratos login succeeded but failed to query local user %s: %v", userID, err)
		logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "query_user_failed")
		return harukiAPIHelper.ErrorInternal(c, "login service unavailable")
	}
	if user.Banned {
		revokeIssuedSession("banned user")
		banMessage := "Your account has been banned"
		if user.BanReason != nil && *user.BanReason != "" {
			banMessage = "Your account has been banned: " + *user.BanReason
		}
		logLogin(harukiAPIHelper.SystemLogResultFailure, user.ID, string(user.Role), "banned")
		return harukiAPIHelper.ErrorForbidden(c, banMessage)
	}

	finalizeLoginRateLimitReservation(c, apiHelper, payload.Email)
	logLogin(harukiAPIHelper.SystemLogResultSuccess, user.ID, string(user.Role), "ok")
	ud := harukiAPIHelper.BuildUserDataFromDBUser(user, &sessionToken)
	resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "login success", UserData: ud}
	return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
}
