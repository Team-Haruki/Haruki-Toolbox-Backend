package adminusers

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleSoftDeleteUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload adminSoftDeletePayload
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&payload); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
			}
		}
		reason, err := sanitizeBanReason(payload.Reason)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidReason, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid reason")
		}

		targetUser, err := queryAdminTargetUser(c, apiHelper, targetUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		banReason := buildSoftDeleteBanReason(reason)
		affected, err := applyManagedTargetUserUpdateGuards(
			apiHelper.DBManager.DB.User.Update().
				SetBanned(true).
				SetBanReason(banReason),
			actorUserID,
			actorRole,
			targetUser.ID,
		).Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to soft delete user")
		}
		if affected == 0 {
			missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to soft delete user")
		}

		updated, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUser.ID)).
			Select(userSchema.FieldID, userSchema.FieldBanned, userSchema.FieldBanReason).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		resp := adminLifecycleResponse{
			UserID:    updated.ID,
			Banned:    updated.Banned,
			BanReason: updated.BanReason,
		}
		clearedSessions := true
		resp.ClearedSessions = &clearedSessions
		revokedOAuthTokens := true
		sessionClearFailed, oauthRevokeFailed := cleanupManagedUserAccessAfterBan(c.Context(), apiHelper, targetUser.ID, targetUser.KratosIdentityID)
		if sessionClearFailed {
			clearedSessions = false
			resp.ClearedSessions = &clearedSessions
		}
		if oauthRevokeFailed {
			revokedOAuthTokens = false
		}
		if !clearedSessions || !revokedOAuthTokens {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
				"hasReason":          reason != nil,
				"sessionClearFailed": sessionClearFailed,
				"oauthRevokeFailed":  oauthRevokeFailed,
				"clearedSessions":    clearedSessions,
				"revokedOAuthTokens": revokedOAuthTokens,
			})
			message, _ := resolveManagedUserBanFinalizeOutcome(sessionClearFailed, oauthRevokeFailed)
			return harukiAPIHelper.SuccessResponse(c, strings.Replace(message, "user banned", "user soft deleted", 1), &resp)
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserSoftDelete, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"hasReason":       reason != nil,
			"clearedSessions": true,
		})
		return harukiAPIHelper.SuccessResponse(c, "user soft deleted", &resp)
	}
}
