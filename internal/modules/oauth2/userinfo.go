package oauth2

import (
	"fmt"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

type oauth2UserProfileResponse struct {
	UserID     string  `json:"userId"`
	Name       string  `json:"name"`
	AvatarPath *string `json:"avatarPath"`
}

type oauth2BindingResponse struct {
	Server   string `json:"server"`
	UserID   string `json:"userId"`
	Verified bool   `json:"verified"`
}

func handleOAuth2GetUserProfile(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, avatarBaseURL string) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		u, err := apiHelper.DBManager.DB.User.Query().
			Where(user.IDEQ(userID)).
			Only(ctx)
		if err != nil {
			if !postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorInternal(c, "failed to query user")
			}
			return harukiAPIHelper.ErrorNotFound(c, "user not found")
		}

		var avatarURL *string
		if u.AvatarPath != nil && *u.AvatarPath != "" {
			full := fmt.Sprintf("%s/avatars/%s", avatarBaseURL, *u.AvatarPath)
			avatarURL = &full
		}

		resp := oauth2UserProfileResponse{
			UserID:     u.ID,
			Name:       u.Name,
			AvatarPath: avatarURL,
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func handleOAuth2GetBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		bindings, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(gameaccountbinding.HasUserWith(user.IDEQ(userID))).
			All(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query bindings")
		}

		resp := make([]oauth2BindingResponse, 0, len(bindings))
		for _, b := range bindings {
			resp = append(resp, oauth2BindingResponse{
				Server:   b.Server,
				UserID:   b.GameUserID,
				Verified: b.Verified,
			})
		}

		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func registerOAuth2UserInfoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, avatarBaseURL string) {
	apiHelper.Router.Get("/api/oauth2/user/profile",
		harukiOAuth2.VerifyOAuth2Token(hydraConfig, apiHelper.DBManager.DB, harukiOAuth2.ScopeUserRead, checkHydraOAuth2ClientActive(hydraConfig)),
		handleOAuth2GetUserProfile(apiHelper, avatarBaseURL),
	)
	apiHelper.Router.Get("/api/oauth2/user/bindings",
		harukiOAuth2.VerifyOAuth2Token(hydraConfig, apiHelper.DBManager.DB, harukiOAuth2.ScopeBindingsRead, checkHydraOAuth2ClientActive(hydraConfig)),
		handleOAuth2GetBindings(apiHelper),
	)
}
