package user

import (
	"context"
	"haruki-suite/utils"
	"haruki-suite/utils/cloudflare"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

func RegisterLoginRoutes(helper HarukiToolboxUserRouterHelpers) {
	helper.Router.Post("/api/user/login", func(c *fiber.Ctx) error {
		ctx := context.Background()
		var payload LoginPayload
		if err := c.BodyParser(&payload); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
		}

		result, err := cloudflare.ValidateTurnstile(payload.ChallengeToken, c.Get("X-Forwarded-For"))
		if err != nil || result == nil || !result.Success {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid Turnstile challenge"})
		}

		user, err := helper.DBManager.DB.User.
			Query().
			Where(userSchema.EmailEQ(payload.Email)).
			WithEmailInfo().
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			Only(ctx)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "Invalid email or password", nil)
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "Invalid email or password", nil)
		}

		sessionToken, err := helper.SessionHandler.IssueSession(user.ID)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Could not issue session", nil)
		}

		var emailInfo EmailInfo
		if user.Edges.EmailInfo != nil {
			emailInfo = EmailInfo{
				Email:    user.Edges.EmailInfo.Email,
				Verified: user.Edges.EmailInfo.Verified,
			}
		} else {
			emailInfo = EmailInfo{
				Email:    payload.Email,
				Verified: false,
			}
		}

		var socialPlatformInfo *SocialPlatformInfo
		if user.Edges.SocialPlatformInfo != nil {
			socialPlatformInfo = &SocialPlatformInfo{
				Platform: user.Edges.SocialPlatformInfo.Platform,
				UserID:   user.Edges.SocialPlatformInfo.PlatformUserID,
				Verified: user.Edges.SocialPlatformInfo.Verified,
			}
		}

		var authorizeSocialPlatformInfo []AuthorizeSocialPlatformInfo
		if user.Edges.AuthorizedSocialPlatforms != nil && len(user.Edges.AuthorizedSocialPlatforms) > 0 {
			authorizeSocialPlatformInfo = make([]AuthorizeSocialPlatformInfo, 0, len(user.Edges.AuthorizedSocialPlatforms))
			for _, a := range user.Edges.AuthorizedSocialPlatforms {
				authorizeSocialPlatformInfo = append(authorizeSocialPlatformInfo, AuthorizeSocialPlatformInfo{
					ID:       a.ID,
					Platform: a.Platform,
					UserID:   a.UserID,
					Comment:  a.Comment,
				})
			}
		}

		var gameAccountBindings []GameAccountBinding
		if user.Edges.GameAccountBindings != nil && len(user.Edges.GameAccountBindings) > 0 {
			gameAccountBindings = make([]GameAccountBinding, 0, len(user.Edges.GameAccountBindings))
			for _, g := range user.Edges.GameAccountBindings {
				gameAccountBindings = append(gameAccountBindings, GameAccountBinding{
					ID:       g.ID,
					Server:   utils.SupportedDataUploadServer(g.Server),
					UserID:   g.GameUserID,
					Verified: g.Verified,
				})
			}
		}

		ud := HarukiToolboxUserData{
			Name:                        user.Name,
			UserID:                      user.ID,
			AvatarPath:                  user.AvatarPath,
			EmailInfo:                   emailInfo,
			SocialPlatformInfo:          socialPlatformInfo,
			AuthorizeSocialPlatformInfo: authorizeSocialPlatformInfo,
			GameAccountBindings:         gameAccountBindings,
			SessionToken:                sessionToken,
		}
		resp := RegisterOrLoginSuccessResponse{Status: fiber.StatusOK, Message: "login success", UserData: ud}
		return ResponseWithStruct(c, fiber.StatusOK, &resp)
	})
}
