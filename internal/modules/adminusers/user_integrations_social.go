package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleGetUserSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserSocialGet
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		info, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQuerySocialPlatformFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}

		resp := adminUserSocialPlatformResponse{
			GeneratedAt: adminNowUTC(),
			UserID:      targetUser.ID,
			Exists:      info != nil,
		}
		if info != nil {
			resp.SocialPlatform = &harukiAPIHelper.SocialPlatformInfo{
				Platform: info.Platform,
				UserID:   info.PlatformUserID,
				Verified: info.Verified,
			}
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"exists": resp.Exists,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpsertUserSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserSocialUpsert
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		payload, err := parseAdminManagedSocialPlatformPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		conflictExists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(
				socialplatforminfo.PlatformEQ(payload.Platform),
				socialplatforminfo.PlatformUserIDEQ(payload.UserID),
				socialplatforminfo.Not(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))),
			).
			Exist(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryConflictFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to check social platform conflict")
		}
		if conflictExists {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonSocialPlatformConflict, nil))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "social platform already bound by another user", nil)
		}

		existing, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQuerySocialPlatformFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}

		created := false
		if existing != nil {
			if _, err := existing.Update().
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetVerified(*payload.Verified).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonSocialPlatformConflict, nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "social platform conflict", nil)
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateSocialPlatformFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
			}
		} else {
			created = true
			if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.Create().
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetVerified(*payload.Verified).
				SetUserSocialPlatformInfo(targetUser.ID).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonSocialPlatformConflict, nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "social platform conflict", nil)
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateSocialPlatformFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create social platform info")
			}
		}

		resp := adminUserSocialPlatformResponse{
			GeneratedAt: adminNowUTC(),
			UserID:      targetUser.ID,
			Exists:      true,
			SocialPlatform: &harukiAPIHelper.SocialPlatformInfo{
				Platform: payload.Platform,
				UserID:   payload.UserID,
				Verified: *payload.Verified,
			},
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"platform": payload.Platform,
			"created":  created,
			"verified": *payload.Verified,
		})
		return harukiAPIHelper.SuccessResponse(c, "social platform upserted", &resp)
	}
}

func handleClearUserSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserSocialClear
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		affected, err := apiHelper.DBManager.DB.SocialPlatformInfo.Delete().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			Exec(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClearSocialPlatformFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to clear social platform info")
		}

		resp := adminUserSocialPlatformResponse{
			GeneratedAt: adminNowUTC(),
			UserID:      targetUser.ID,
			Exists:      false,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"deleted": affected > 0,
		})
		return harukiAPIHelper.SuccessResponse(c, "social platform cleared", &resp)
	}
}

func handleListUserAuthorizedSocialPlatforms(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserAuthorizedList
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		rows, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
			Where(authorizesocialplatforminfo.UserIDEQ(targetUser.ID)).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizedSocialPlatformsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorized social platforms")
		}

		resp := adminUserAuthorizedSocialListResponse{
			GeneratedAt: adminNowUTC(),
			UserID:      targetUser.ID,
			Total:       len(rows),
			Items:       buildAdminAuthorizedSocialItems(rows),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpsertUserAuthorizedSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserAuthorizedUpsert
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		platformID64, err := strconv.ParseInt(strings.TrimSpace(c.Params("platform_id")), 10, 64)
		platformID := int(platformID64)
		if err != nil || platformID <= 0 {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPlatformId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "platform_id must be positive integer")
		}

		payload, err := parseAdminManagedAuthorizedSocialPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		existing, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
			Where(
				authorizesocialplatforminfo.UserIDEQ(targetUser.ID),
				authorizesocialplatforminfo.PlatformIDEQ(platformID),
			).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizedSocialPlatformFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorized social platform")
		}

		created := false
		if existing != nil {
			if _, err := existing.Update().
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetComment(payload.Comment).
				Save(c.Context()); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateAuthorizedSocialPlatformFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update authorized social platform")
			}
		} else {
			created = true
			if _, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Create().
				SetUserID(targetUser.ID).
				SetPlatform(payload.Platform).
				SetPlatformUserID(payload.UserID).
				SetPlatformID(platformID).
				SetComment(payload.Comment).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAuthorizedSocialPlatformConflict, nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "authorized social platform conflict", nil)
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateAuthorizedSocialPlatformFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create authorized social platform")
			}
		}

		resp := adminUserAuthorizedSocialUpsertResponse{
			UserID:     targetUser.ID,
			PlatformID: platformID,
			Created:    created,
			Record: harukiAPIHelper.AuthorizeSocialPlatformInfo{
				PlatformID: platformID,
				Platform:   payload.Platform,
				UserID:     payload.UserID,
				Comment:    payload.Comment,
			},
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"platformID": platformID,
			"created":    created,
		})
		return harukiAPIHelper.SuccessResponse(c, "authorized social platform upserted", &resp)
	}
}

func handleDeleteUserAuthorizedSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserAuthorizedDelete
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		platformID64, err := strconv.ParseInt(strings.TrimSpace(c.Params("platform_id")), 10, 64)
		platformID := int(platformID64)
		if err != nil || platformID <= 0 {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPlatformId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "platform_id must be positive integer")
		}

		affected, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Delete().
			Where(
				authorizesocialplatforminfo.UserIDEQ(targetUser.ID),
				authorizesocialplatforminfo.PlatformIDEQ(platformID),
			).
			Exec(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteAuthorizedSocialPlatformFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete authorized social platform")
		}
		if affected == 0 {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAuthorizedSocialPlatformNotFound, nil))
			return harukiAPIHelper.ErrorNotFound(c, "authorized social platform not found")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"platformID": platformID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "authorized social platform deleted", nil)
	}
}

func handleRegenerateUserIOSUploadCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserIOSCodeRegenerate
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		code, err := generateAdminIOSUploadCode()
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonGenerateUploadCodeFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to generate upload code")
		}

		existing, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UserIDEQ(targetUser.ID)).
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryIosUploadCodeFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query upload code")
		}

		if existing != nil {
			if _, err := existing.Update().SetUploadCode(code).Save(c.Context()); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateIosUploadCodeFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update upload code")
			}
		} else {
			if _, err := apiHelper.DBManager.DB.IOSScriptCode.Create().
				SetUserID(targetUser.ID).
				SetUploadCode(code).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonIosUploadCodeConflict, nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "upload code conflict", nil)
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateIosUploadCodeFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create upload code")
			}
		}

		resp := adminUserIOSUploadCodeResponse{UserID: targetUser.ID, UploadCode: code}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "ios upload code regenerated", &resp)
	}
}

func handleClearUserIOSUploadCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserIOSCodeClear
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		affected, err := apiHelper.DBManager.DB.IOSScriptCode.Delete().
			Where(iosscriptcode.UserIDEQ(targetUser.ID)).
			Exec(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClearIosUploadCodeFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to clear ios upload code")
		}

		resp := adminUserClearIOSUploadCodeResponse{
			UserID:  targetUser.ID,
			Cleared: affected > 0,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"cleared": resp.Cleared,
		})
		return harukiAPIHelper.SuccessResponse(c, "ios upload code cleared", &resp)
	}
}
