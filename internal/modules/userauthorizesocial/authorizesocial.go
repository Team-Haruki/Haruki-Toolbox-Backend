package userauthorizesocial

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
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

func buildAuthorizedSocialPlatformUserData(
	ctx fiber.Ctx,
	client *postgresql.AuthorizeSocialPlatformInfoClient,
	toolboxUserID string,
) (*harukiAPIHelper.HarukiToolboxUserData, error) {
	infos, err := client.Query().
		Where(authorizesocialplatforminfo.UserID(toolboxUserID)).
		All(ctx.Context())
	if err != nil {
		return nil, err
	}
	resp := make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(infos))
	for _, i := range infos {
		resp = append(resp, harukiAPIHelper.AuthorizeSocialPlatformInfo{
			PlatformID:            i.PlatformID,
			Platform:              i.Platform,
			UserID:                i.PlatformUserID,
			Comment:               i.Comment,
			AllowFastVerification: i.AllowFastVerification,
		})
	}
	ud := harukiAPIHelper.HarukiToolboxUserData{
		AuthorizeSocialPlatformInfo: &resp,
	}
	return &ud, nil
}

func parseAuthorizeSocialPlatformID(c fiber.Ctx) (int, error) {
	idParam := c.Params("id")
	userAccountID64, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return 0, err
	}
	userAccountID := int(userAccountID64)
	if userAccountID <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "invalid id parameter")
	}
	return userAccountID, nil
}

func parseAuthorizeSocialPlatformPayload(c fiber.Ctx) (harukiAPIHelper.AuthorizeSocialPlatformPayload, error) {
	var payload harukiAPIHelper.AuthorizeSocialPlatformPayload
	if err := c.Bind().Body(&payload); err != nil {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, err
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatform(payload.Platform)) {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, fiber.NewError(fiber.StatusBadRequest, "unsupported platform")
	}
	return payload, nil
}

func handleUpdateAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.authorized_social_platform.update", result, toolboxUserID, map[string]any{
				"reason": reason,
			})
		}()

		userAccountID, err := parseAuthorizeSocialPlatformID(c)
		if err != nil {
			reason = "invalid_platform_id"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id parameter")
		}
		payload, err := parseAuthorizeSocialPlatformPayload(c)
		if err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo
		existing, err := client.Query().
			Where(
				authorizesocialplatforminfo.UserID(toolboxUserID),
				authorizesocialplatforminfo.PlatformIDEQ(userAccountID),
			).
			Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				reason = "authorized_social_platform_not_found"
				return harukiAPIHelper.ErrorNotFound(c, "authorized social platform not found")
			}
			harukiLogger.Errorf("Failed to query authorized social platform: %v", err)
			reason = "query_authorized_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform")
		}

		_, err = client.UpdateOne(existing).
			SetPlatform(payload.Platform).
			SetPlatformUserID(payload.UserID).
			SetComment(payload.Comment).
			SetAllowFastVerification(payload.AllowFastVerification).
			Save(ctx)
		if err != nil {
			if postgresql.IsConstraintError(err) {
				reason = "authorized_social_platform_conflict"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "authorized social platform conflict", nil)
			}
			harukiLogger.Errorf("Failed to update authorized social platform: %v", err)
			reason = "update_authorized_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform")
		}
		ud, err := buildAuthorizedSocialPlatformUserData(c, client, toolboxUserID)
		if err != nil {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			reason = "query_authorized_social_platforms_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch authorized social platforms")
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform updated", ud)
	}
}

func handleCreateAuthorizeSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.authorized_social_platform.create", result, toolboxUserID, map[string]any{
				"reason": reason,
			})
		}()

		payload, err := parseAuthorizeSocialPlatformPayload(c)
		if err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
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
			reason = "query_authorized_social_platforms_failed"
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
			SetAllowFastVerification(payload.AllowFastVerification).
			SetNillableComment(func() *string {
				if payload.Comment == "" {
					return nil
				}
				return &payload.Comment
			}()).
			Save(ctx)
		if err != nil {
			if postgresql.IsConstraintError(err) {
				reason = "authorized_social_platform_conflict"
				return harukiAPIHelper.ErrorBadRequest(c, "a social platform entry with this platform_id already exists for the user")
			}
			harukiLogger.Errorf("Failed to create authorized social platform: %v", err)
			reason = "create_authorized_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to create social platform")
		}
		ud, err := buildAuthorizedSocialPlatformUserData(c, client, toolboxUserID)
		if err != nil {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			reason = "query_authorized_social_platforms_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch authorized social platforms")
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform created", ud)
	}
}

func handleCreateAuthorizeSocialPlatformAtID(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := c.Params("toolbox_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.authorized_social_platform.create", result, toolboxUserID, map[string]any{
				"reason": reason,
			})
		}()

		userAccountID, err := parseAuthorizeSocialPlatformID(c)
		if err != nil {
			reason = "invalid_platform_id"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id parameter")
		}
		payload, err := parseAuthorizeSocialPlatformPayload(c)
		if err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo
		_, err = client.Create().
			SetUserID(toolboxUserID).
			SetPlatform(payload.Platform).
			SetPlatformUserID(payload.UserID).
			SetPlatformID(userAccountID).
			SetAllowFastVerification(payload.AllowFastVerification).
			SetNillableComment(func() *string {
				if payload.Comment == "" {
					return nil
				}
				return &payload.Comment
			}()).
			Save(ctx)
		if err != nil {
			if postgresql.IsConstraintError(err) {
				reason = "authorized_social_platform_conflict"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "authorized social platform conflict", nil)
			}
			harukiLogger.Errorf("Failed to create authorized social platform: %v", err)
			reason = "create_authorized_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to create social platform")
		}

		ud, err := buildAuthorizedSocialPlatformUserData(c, client, toolboxUserID)
		if err != nil {
			harukiLogger.Errorf("Failed to query authorized social platforms: %v", err)
			reason = "query_authorized_social_platforms_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch authorized social platforms")
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform created", ud)
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
		authorizeSocialPlatformAccountID64, err := strconv.ParseInt(idParam, 10, 64)
		if err != nil {
			reason = "invalid_platform_id"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id parameter")
		}
		authorizeSocialPlatformAccountID := int(authorizeSocialPlatformAccountID64)
		ctx := c.Context()
		client := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo
		affected, err := client.Delete().
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
		if affected == 0 {
			reason = "authorized_social_platform_not_found"
			return harukiAPIHelper.ErrorNotFound(c, "authorized social platform not found")
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
				PlatformID:            i.PlatformID,
				Platform:              i.Platform,
				UserID:                i.PlatformUserID,
				Comment:               i.Comment,
				AllowFastVerification: i.AllowFastVerification,
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
	base := apiHelper.Router.Group("/api/user/:toolbox_user_id/authorize-social-platform")
	requireVerifiedEmail := userCoreModule.RequireVerifiedEmail()

	base.Post("/", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), requireVerifiedEmail, verifyUserHasVerifiedSocialPlatform(apiHelper), handleCreateAuthorizeSocialPlatform(apiHelper))

	r := base.Group("/:id")
	r.RouteChain("/").
		Post(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), requireVerifiedEmail, verifyUserHasVerifiedSocialPlatform(apiHelper), handleCreateAuthorizeSocialPlatformAtID(apiHelper)).
		Put(apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), requireVerifiedEmail, verifyUserHasVerifiedSocialPlatform(apiHelper), handleUpdateAuthorizeSocialPlatform(apiHelper)).
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
