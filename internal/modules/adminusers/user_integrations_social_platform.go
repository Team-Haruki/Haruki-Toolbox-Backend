package adminusers

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

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
