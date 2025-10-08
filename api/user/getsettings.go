package user

import (
	"context"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v2"
)

func registerGetInfoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Group("/api/user/:toolbox_user_id/get-settings", apiHelper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("UserID").(string)

		user, err := apiHelper.DBManager.DB.User.
			Query().
			Where(userSchema.IDEQ(userID)).
			WithEmailInfo().
			WithSocialPlatformInfo().
			WithAuthorizedSocialPlatforms().
			WithGameAccountBindings().
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid email or password", nil)
		}

		var emailInfo harukiAPIHelper.EmailInfo
		if user.Edges.EmailInfo != nil {
			emailInfo = harukiAPIHelper.EmailInfo{
				Email:    user.Edges.EmailInfo.Email,
				Verified: user.Edges.EmailInfo.Verified,
			}
		} else {
			emailInfo = harukiAPIHelper.EmailInfo{
				Email:    user.Edges.EmailInfo.Email,
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
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "success get latest settings", &ud)

	})
}
