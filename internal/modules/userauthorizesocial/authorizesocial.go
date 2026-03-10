package userauthorizesocial

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
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
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorBadRequest(c, "user has no verified social platform info")
			}
			harukiLogger.Errorf("Failed to query verified social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}
		if info == nil {
			return harukiAPIHelper.ErrorBadRequest(c, "user has no verified social platform info")
		}
		return c.Next()
	}
}

func handleAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.authorized_social_platform.upsert", result, toolboxUserID, map[string]any{
				"reason": reason,
			})
		}()

		idParam := c.Params("id")
		userAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			reason = "invalid_platform_id"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id parameter")
		}
		var payload harukiAPIHelper.AuthorizeSocialPlatformPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}

		if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatform(payload.Platform)) {
			reason = "unsupported_platform"
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
			reason = "already_authorized"
			return harukiAPIHelper.ErrorBadRequest(c, "this social platform account is authorized")
		}
		if err != nil && !postgresql.IsNotFound(err) {
			harukiLogger.Errorf("Failed to query authorized social platform before create: %v", err)
			reason = "query_authorized_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorized social platform")
		}
		_, err = client.Create().
			SetUserID(toolboxUserID).
			SetPlatformID(userAccountID).
			SetPlatform(payload.Platform).
			SetPlatformUserID(payload.UserID).
			SetComment(payload.Comment).
			Save(ctx)
		if err != nil {
			if postgresql.IsConstraintError(err) {
				reason = "authorized_social_platform_conflict"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "authorized social platform conflict", nil)
			}
			harukiLogger.Errorf("Failed to create authorized social platform: %v", err)
			reason = "create_authorized_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to add social platform")
		}
		infos, err := client.Query().
			Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
			All(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			reason = "query_authorized_social_platforms_failed"
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
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform updated", &ud)
	}
}

func handleDeleteAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.authorized_social_platform.delete", result, toolboxUserID, map[string]any{
				"reason": reason,
			})
		}()

		idParam := c.Params("id")
		authorizeSocialPlatformAccountID, err := strconv.Atoi(idParam)
		if err != nil {
			reason = "invalid_platform_id"
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
			reason = "delete_authorized_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to delete authorized social platform")
		}
		infos, err := client.Query().
			Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
			All(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			reason = "query_authorized_social_platforms_failed"
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
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform updated", &ud)
	}
}

func RegisterUserAuthorizeSocialRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/authorize-social-platform/:id")

	r.RouteChain("/").
		Put(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), verifyUserHasVerifiedSocialPlatform(apiHelper), handleAuthorizeSocialPlatform(apiHelper)).
		Delete(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), verifyUserHasVerifiedSocialPlatform(apiHelper), handleDeleteAuthorizeSocialPlatform(apiHelper))
}

func isSupportedSocialPlatform(platform harukiAPIHelper.SocialPlatform) bool {
	switch platform {
	case harukiAPIHelper.SocialPlatformQQ,
		harukiAPIHelper.SocialPlatformQQBot,
		harukiAPIHelper.SocialPlatformDiscord,
		harukiAPIHelper.SocialPlatformTelegram:
		return true
	default:
		return false
	}
}
