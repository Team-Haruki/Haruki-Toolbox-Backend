package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleGetUserRole(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := c.Params("target_user_id")
		if targetUserID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query user role")
		}

		resp := userRoleResponse{
			UserID: dbUser.ID,
			Role:   adminCoreModule.NormalizeRole(string(dbUser.Role)),
			Banned: dbUser.Banned,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateUserRole(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload updateUserRolePayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		normalizedRole := adminCoreModule.NormalizeRole(payload.Role)
		if !adminCoreModule.IsValidRole(normalizedRole) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRole, map[string]any{
				"requestedRole": strings.TrimSpace(payload.Role),
			}))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid role")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		affected, err := applyManagedTargetUserUpdateGuards(
			apiHelper.DBManager.DB.User.Update().SetRole(userSchema.Role(normalizedRole)),
			actorUserID,
			actorRole,
			targetUser.ID,
		).Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateRoleFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update user role")
		}
		if affected == 0 {
			missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to update user role")
		}

		updatedUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUser.ID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query user")
		}

		resp := userRoleResponse{
			UserID: updatedUser.ID,
			Role:   adminCoreModule.NormalizeRole(string(updatedUser.Role)),
			Banned: updatedUser.Banned,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRoleUpdate, adminAuditTargetTypeUser, updatedUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"newRole": resp.Role,
		})
		return harukiAPIHelper.SuccessResponse(c, "role updated", &resp)
	}
}
