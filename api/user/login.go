package user

import (
	"context"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/cloudflare"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

func registerLoginRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Post("/api/user/login", func(c *fiber.Ctx) error {
		ctx := context.Background()
		var payload harukiAPIHelper.LoginPayload
		if err := c.BodyParser(&payload); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
		}

		result, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, c.Get("X-Forwarded-For"))
		if err != nil || result == nil || !result.Success {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid Turnstile challenge"})
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
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "Invalid email or password", nil)
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "Invalid email or password", nil)
		}

		sessionToken, err := apiHelper.SessionHandler.IssueSession(user.ID)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Could not issue session", nil)
		}

		var emailInfo harukiAPIHelper.EmailInfo
		if user.Edges.EmailInfo != nil {
			emailInfo = harukiAPIHelper.EmailInfo{
				Email:    user.Edges.EmailInfo.Email,
				Verified: user.Edges.EmailInfo.Verified,
			}
		} else {
			emailInfo = harukiAPIHelper.EmailInfo{
				Email:    payload.Email,
				Verified: false,
			}
		}

		var socialPlatformInfo *harukiAPIHelper.SocialPlatformInfo
		if user.Edges.SocialPlatformInfo != nil {
			socialPlatformInfo = &harukiAPIHelper.SocialPlatformInfo{
				Platform: user.Edges.SocialPlatformInfo.Platform,
				UserID:   user.Edges.SocialPlatformInfo.PlatformUserID,
				Verified: user.Edges.SocialPlatformInfo.Verified,
			}
		}
		var authorizeSocialPlatformInfo []harukiAPIHelper.AuthorizeSocialPlatformInfo
		if user.Edges.AuthorizedSocialPlatforms != nil && len(user.Edges.AuthorizedSocialPlatforms) > 0 {
			authorizeSocialPlatformInfo = make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(user.Edges.AuthorizedSocialPlatforms))
			for _, a := range user.Edges.AuthorizedSocialPlatforms {
				authorizeSocialPlatformInfo = append(authorizeSocialPlatformInfo, harukiAPIHelper.AuthorizeSocialPlatformInfo{
					ID:       a.ID,
					Platform: a.Platform,
					UserID:   a.PlatformUserID,
					Comment:  a.Comment,
				})
			}
		}

		var gameAccountBindings []harukiAPIHelper.GameAccountBinding
		if user.Edges.GameAccountBindings != nil && len(user.Edges.GameAccountBindings) > 0 {
			gameAccountBindings = make([]harukiAPIHelper.GameAccountBinding, 0, len(user.Edges.GameAccountBindings))
			for _, g := range user.Edges.GameAccountBindings {
				gameAccountBindings = append(gameAccountBindings, harukiAPIHelper.GameAccountBinding{
					Server:   utils.SupportedDataUploadServer(g.Server),
					UserID:   g.GameUserID,
					Verified: g.Verified,
					Suite:    g.Suite,
					Mysekai:  g.Mysekai,
				})
			}
		}

		var avatarURL string
		if user.AvatarPath != nil {
			avatarURL = fmt.Sprintf("%s/avatars/%s", config.Cfg.UserSystem.FrontendURL, *user.AvatarPath)
		} else {
			avatarURL = ""
		}
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
	})
}
