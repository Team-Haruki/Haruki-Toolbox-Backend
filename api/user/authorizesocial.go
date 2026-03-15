package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"

	"entgo.io/ent/dialect/sql"
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

		switch harukiAPIHelper.SocialPlatform(payload.Platform) {
		case harukiAPIHelper.SocialPlatformQQ, harukiAPIHelper.SocialPlatformQQBot,
			harukiAPIHelper.SocialPlatformDiscord, harukiAPIHelper.SocialPlatformTelegram:

		default:
			return harukiAPIHelper.ErrorBadRequest(c, "unsupported platform")
		}
		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo
		existing, err := client.Query().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.PlatformID(userAccountID),
			).
			Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorBadRequest(c, "social platform not found")
			}
			harukiLogger.Errorf("Failed to query authorized social platform: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform")
		}
		_, err = client.UpdateOne(existing).
			SetPlatform(payload.Platform).
			SetPlatformUserID(payload.UserID).
			SetComment(payload.Comment).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update authorized social platform: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform")
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
				PlatformID: i.PlatformID,
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

func handleCreateAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		var payload harukiAPIHelper.AuthorizeSocialPlatformPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}

		switch harukiAPIHelper.SocialPlatform(payload.Platform) {
		case harukiAPIHelper.SocialPlatformQQ, harukiAPIHelper.SocialPlatformQQBot,
			harukiAPIHelper.SocialPlatformDiscord, harukiAPIHelper.SocialPlatformTelegram:

		default:
			return harukiAPIHelper.ErrorBadRequest(c, "unsupported platform")
		}
		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo
		newPlatformID := 1
		latest, err := client.Query().
			Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
			Order(authorizesocialplatforminfo.ByPlatformID(sql.OrderDesc())).
			First(ctx)
		if err != nil && !postgresql.IsNotFound(err) {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platforms")
		}
		if latest != nil {
			newPlatformID = latest.PlatformID + 1
		}
		_, err = client.Create().
			SetUserID(toolboxUserID).
			SetPlatform(payload.Platform).
			SetPlatformUserID(payload.UserID).
			SetPlatformID(newPlatformID).
			SetNillableComment(func() *string {
				if payload.Comment == "" {
					return nil
				}
				return &payload.Comment
			}()).
			Save(ctx)
		if err != nil {
			if postgresql.IsConstraintError(err) {
				return harukiAPIHelper.ErrorBadRequest(c, "a social platform entry with this platform_id already exists for the user")
			}
			harukiLogger.Errorf("Failed to create authorized social platform: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to create social platform")
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
				PlatformID: i.PlatformID,
				Platform:   i.Platform,
				UserID:     i.PlatformUserID,
				Comment:    i.Comment,
			})
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{
			AuthorizeSocialPlatformInfo: &resp,
		}
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform created", &ud)
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
				PlatformID: i.PlatformID,
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
	base := apiHelper.Router.Group("/api/user/:toolbox_user_id/authorize-social-platform")

	base.Post("/", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), verifyUserHasVerifiedSocialPlatform(apiHelper), handleCreateAuthorizeSocialPlatform(apiHelper))

	r := base.Group("/:id")
	r.RouteChain("/").
		Put(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), verifyUserHasVerifiedSocialPlatform(apiHelper), handleAuthorizeSocialPlatform(apiHelper)).
		Delete(apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), verifyUserHasVerifiedSocialPlatform(apiHelper), handleDeleteAuthorizeSocialPlatform(apiHelper))
}
