package adminusers

import (
	"strconv"
	"strings"

	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"

	"github.com/gofiber/fiber/v3"
)

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
