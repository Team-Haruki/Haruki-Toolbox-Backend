package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

func handleLogin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var payload harukiAPIHelper.LoginPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid request")
		}

		result, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, c.IP())
		if err != nil || result == nil || !result.Success {
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid Turnstile challenge")
		}

		user, err := apiHelper.DBManager.DB.User.
			Query().
			Where(userSchema.EmailEQ(payload.Email)).
			WithEmailInfo().
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			Only(ctx)
		if err != nil {
			harukiLogger.Infof("Login failed for email %s: user not found or query error", payload.Email)
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
			harukiLogger.Infof("Login failed for email %s: invalid password", payload.Email)
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid email or password")
		}

		sessionToken, err := apiHelper.SessionHandler.IssueSession(user.ID)
		if err != nil {
			harukiLogger.Errorf("Failed to issue session for user %s: %v", user.ID, err)
			return harukiAPIHelper.ErrorInternal(c, "Could not issue session")
		}

		ud := harukiAPIHelper.BuildUserDataFromDBUser(user, &sessionToken)
		resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "login success", UserData: ud}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
	}
}

func registerLoginRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/login", handleLogin(apiHelper))
}
