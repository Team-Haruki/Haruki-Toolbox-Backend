package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func verifyUserHasVerifiedSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		ctx := c.Context()
		client := apiHelper.DBManager.DB.SocialPlatformInfo
		info, err := client.Query().
			Where(
				socialplatforminfo.UserSocialPlatformInfoEQ(toolboxUserID),
				socialplatforminfo.Verified(true),
			).First(ctx)
		if err != nil || info == nil {
			return harukiAPIHelper.ErrorBadRequest(c, "user has no verified social platform info")
		}
		return c.Next()
	}
}

func handleAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		idParam := c.Params("id")
		userAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id parameter")
		}
		var payload harukiAPIHelper.AuthorizeSocialPlatformPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		// Validate platform
		switch harukiAPIHelper.SocialPlatform(payload.Platform) {
		case harukiAPIHelper.SocialPlatformQQ, harukiAPIHelper.SocialPlatformQQBot,
			harukiAPIHelper.SocialPlatformDiscord, harukiAPIHelper.SocialPlatformTelegram:
			// valid
		default:
			return harukiAPIHelper.ErrorBadRequest(c, "unsupported platform")
		}
		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo
		existing, err := client.Query().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.Platform(payload.Platform),
				authorizesocialplatforminfo.PlatformUserID(payload.UserID),
			).
			Only(ctx)
		if err == nil && existing != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "this social platform account is authorized")
		}
		_, err = client.Create().
			SetUserID(toolboxUserID).
			SetPlatformID(userAccountID).
			SetPlatform(payload.Platform).
			SetPlatformUserID(payload.UserID).
			SetComment(payload.Comment).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to create authorized social platform: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to add social platform")
		}
		infos, err := client.Query().
			Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
			All(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch authorized social platforms")
		}
		resp := make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(infos))
		for _, i := range infos {
			resp = append(resp, harukiAPIHelper.AuthorizeSocialPlatformInfo{
				ID:       i.PlatformID,
				Platform: i.Platform,
				UserID:   i.PlatformUserID,
				Comment:  i.Comment,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			AuthorizeSocialPlatformInfo: &resp,
		}
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform updated", &ud)
	}
}

func handleDeleteAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		idParam := c.Params("id")
		authorizeSocialPlatformAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id parameter")
		}
		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo
		_, err = client.Delete().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.PlatformIDEQ(authorizeSocialPlatformAccountID),
			).
			Exec(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to delete authorized social platform: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to delete authorized social platform")
		}
		infos, err := client.Query().
			Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
			All(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch authorized social platforms")
		}
		resp := make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(infos))
		for _, i := range infos {
			resp = append(resp, harukiAPIHelper.AuthorizeSocialPlatformInfo{
				ID:       i.PlatformID,
				Platform: i.Platform,
				UserID:   i.PlatformUserID,
				Comment:  i.Comment,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			AuthorizeSocialPlatformInfo: &resp,
		}
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform updated", &ud)
	}
}

func registerAuthorizeSocialPlatformRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/authorize-social-platform/:id")

	r.RouteChain("/").
		Put(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), verifyUserHasVerifiedSocialPlatform(apiHelper), handleAuthorizeSocialPlatform(apiHelper)).
		Delete(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), verifyUserHasVerifiedSocialPlatform(apiHelper), handleDeleteAuthorizeSocialPlatform(apiHelper))
}
