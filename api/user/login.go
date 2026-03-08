package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
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
				roleLower := strings.ToLower(strings.TrimSpace(actorRole))
				if roleLower == "" {
					roleLower = "user"
				}
				entry.ActorRole = &roleLower
				if roleLower == "admin" || roleLower == "super_admin" {
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
		result, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, c.IP())
		if err != nil || result == nil || !result.Success {
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_challenge")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid Turnstile challenge")
		}
		user, err := apiHelper.DBManager.DB.User.
			Query().
			Where(userSchema.EmailEQ(payload.Email)).
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			WithIosScriptCode().
			Only(ctx)
		if err != nil {
			harukiLogger.Infof("Login failed for email %s: user not found or query error", payload.Email)
			logLogin(harukiAPIHelper.SystemLogResultFailure, "", "", "invalid_credentials")
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
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
			harukiLogger.Errorf("Failed to issue session for user %s: %v", user.ID, err)
			logLogin(harukiAPIHelper.SystemLogResultFailure, user.ID, string(user.Role), "issue_session_failed")
			return harukiAPIHelper.ErrorInternal(c, "Could not issue session")
		}
		logLogin(harukiAPIHelper.SystemLogResultSuccess, user.ID, string(user.Role), "ok")
		ud := harukiAPIHelper.BuildUserDataFromDBUser(user, &sessionToken)
		resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "login success", UserData: ud}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
	}
}

func registerLoginRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/login", handleLogin(apiHelper))
}
