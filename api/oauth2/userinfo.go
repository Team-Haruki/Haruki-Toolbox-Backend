package oauth2

import (
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/user"
	harukiOAuth2 "haruki-suite/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

// OAuth2 user profile response (scope: user:read)
type oauth2UserProfileResponse struct {
	UserID     string  `json:"userId"`
	Name       string  `json:"name"`
	AvatarPath *string `json:"avatarPath"`
}

// OAuth2 binding response (scope: bindings:read)
type oauth2BindingResponse struct {
	Server   string `json:"server"`
	UserID   string `json:"userId"`
	Verified bool   `json:"verified"`
}

// handleOAuth2GetUserProfile returns the authenticated user's name and avatar.
// GET /api/oauth2/user/profile
// Scope: user:read
func handleOAuth2GetUserProfile(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)

		u, err := apiHelper.DBManager.DB.User.Query().
			Where(user.IDEQ(userID)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorNotFound(c, "user not found")
		}

		var avatarURL *string
		if u.AvatarPath != nil && *u.AvatarPath != "" {
			full := fmt.Sprintf("%s/avatars/%s", config.Cfg.UserSystem.AvatarURL, *u.AvatarPath)
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

// handleOAuth2GetBindings returns the authenticated user's game account bindings.
// GET /api/oauth2/user/bindings
// Scope: bindings:read
func handleOAuth2GetBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)

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

// registerOAuth2UserInfoRoutes registers OAuth2-protected user info endpoints.
func registerOAuth2UserInfoRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Get("/api/oauth2/user/profile",
		harukiOAuth2.VerifyOAuth2Token(apiHelper.DBManager.DB, harukiOAuth2.ScopeUserRead),
		handleOAuth2GetUserProfile(apiHelper),
	)
	apiHelper.Router.Get("/api/oauth2/user/bindings",
		harukiOAuth2.VerifyOAuth2Token(apiHelper.DBManager.DB, harukiOAuth2.ScopeBindingsRead),
		handleOAuth2GetBindings(apiHelper),
	)
}
