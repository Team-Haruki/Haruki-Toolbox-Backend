package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/iosscriptcode"

	"github.com/gofiber/fiber/v3"
)

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
