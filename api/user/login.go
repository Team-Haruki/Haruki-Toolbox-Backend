package user

import (
	"context"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

func handleLogin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := context.Background()
		var payload harukiAPIHelper.LoginPayload
		if err := c.Bind().Body(&payload); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
		}

		result, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, c.Get("X-Forwarded-For"))
		if err != nil || result == nil || !result.Success {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid Turnstile challenge"})
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
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid email or password", nil)
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid email or password", nil)
		}

		sessionToken, err := apiHelper.SessionHandler.IssueSession(user.ID)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Could not issue session", nil)
		}

		emailInfo := buildEmailInfo(user, payload.Email)
		socialPlatformInfo := buildSocialPlatformInfo(user)
		authorizeSocialPlatformInfo := buildAuthorizeSocialPlatformInfo(user)
		gameAccountBindings := buildGameAccountBindings(user)
		avatarURL := buildAvatarURL(user)

		ud := harukiAPIHelper.HarukiToolboxUserData{
			Name:                        &user.Name,
			UserID:                      &user.ID,
			AvatarPath:                  &avatarURL,
			AllowCNMysekai:              &user.AllowCnMysekai,
			EmailInfo:                   &emailInfo,
			SocialPlatformInfo:          socialPlatformInfo,
			AuthorizeSocialPlatformInfo: &authorizeSocialPlatformInfo,
			GameAccountBindings:         &gameAccountBindings,
			SessionToken:                &sessionToken,
		}
		resp := harukiAPIHelper.RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "login success", UserData: ud}
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, &resp)
	}
}

func buildEmailInfo(user *postgresql.User, email string) harukiAPIHelper.EmailInfo {
	if user.Edges.EmailInfo != nil {
		return harukiAPIHelper.EmailInfo{
			Email:    user.Edges.EmailInfo.Email,
			Verified: user.Edges.EmailInfo.Verified,
		}
	}
	return harukiAPIHelper.EmailInfo{Email: email, Verified: false}
}

func buildSocialPlatformInfo(user *postgresql.User) *harukiAPIHelper.SocialPlatformInfo {
	if user.Edges.SocialPlatformInfo != nil {
		return &harukiAPIHelper.SocialPlatformInfo{
			Platform: user.Edges.SocialPlatformInfo.Platform,
			UserID:   user.Edges.SocialPlatformInfo.PlatformUserID,
			Verified: user.Edges.SocialPlatformInfo.Verified,
		}
	}
	return nil
}

func buildAuthorizeSocialPlatformInfo(user *postgresql.User) []harukiAPIHelper.AuthorizeSocialPlatformInfo {
	var result []harukiAPIHelper.AuthorizeSocialPlatformInfo
	if user.Edges.AuthorizedSocialPlatforms != nil && len(user.Edges.AuthorizedSocialPlatforms) > 0 {
		result = make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(user.Edges.AuthorizedSocialPlatforms))
		for _, a := range user.Edges.AuthorizedSocialPlatforms {
			result = append(result, harukiAPIHelper.AuthorizeSocialPlatformInfo{
				ID:       a.ID,
				Platform: a.Platform,
				UserID:   a.PlatformUserID,
				Comment:  a.Comment,
			})
		}
	}
	return result
}

func buildGameAccountBindings(user *postgresql.User) []harukiAPIHelper.GameAccountBinding {
	var result []harukiAPIHelper.GameAccountBinding
	if user.Edges.GameAccountBindings != nil && len(user.Edges.GameAccountBindings) > 0 {
		result = make([]harukiAPIHelper.GameAccountBinding, 0, len(user.Edges.GameAccountBindings))
		for _, g := range user.Edges.GameAccountBindings {
			result = append(result, harukiAPIHelper.GameAccountBinding{
				Server:   utils.SupportedDataUploadServer(g.Server),
				UserID:   g.GameUserID,
				Verified: g.Verified,
				Suite:    g.Suite,
				Mysekai:  g.Mysekai,
			})
		}
	}
	return result
}

func buildAvatarURL(user *postgresql.User) string {
	if user.AvatarPath != nil {
		return fmt.Sprintf("%s/avatars/%s", config.Cfg.UserSystem.FrontendURL, *user.AvatarPath)
	}
	return ""
}

func registerLoginRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/login", handleLogin(apiHelper))
}
