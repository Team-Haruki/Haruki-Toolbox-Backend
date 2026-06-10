package adminusers

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	userauth "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userauth"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleResetUserPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload adminResetPasswordPayload
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&payload); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
			}
		}

		targetUser, err := queryAdminTargetUser(c, apiHelper, targetUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		password := ""
		temporaryPassword := ""
		if payload.NewPassword != nil {
			password = *payload.NewPassword
		} else {
			generated, err := generateTemporaryPassword()
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonGenerateTemporaryPasswordFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to generate temporary password")
			}
			password = generated
			temporaryPassword = generated
		}

		if err := validateAdminPasswordInput(password); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPassword, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid password")
		}

		forceLogout := true
		if payload.ForceLogout != nil {
			forceLogout = *payload.ForceLogout
		}

		kratosIdentityID := ""
		if targetUser.KratosIdentityID != nil {
			kratosIdentityID = strings.TrimSpace(*targetUser.KratosIdentityID)
		}
		kratosManaged := apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() && kratosIdentityID != ""
		if kratosManaged {
			if err := apiHelper.SessionHandler.UpdateKratosPasswordByIdentityID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), kratosIdentityID, password); err != nil {
				switch {
				case harukiAPIHelper.IsKratosIdentityUnmappedError(err):
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorNotFound(c, "user identity not found")
				case harukiAPIHelper.IsIdentityProviderUnavailableError(err):
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonIdentityProviderUnavailable, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorInternal(c, "identity provider unavailable")
				default:
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdatePasswordFailed, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorInternal(c, "failed to reset password")
				}
			}
		}

		localMirrorFailed := false
		localMirrorFailureReason := ""
		if !kratosManaged {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, map[string]any{"reason": "managed_identity_required"}))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusGone, userauth.ManagedIdentityMessage, nil)
		}
		resp := adminResetPasswordResponse{
			UserID:            targetUser.ID,
			TemporaryPassword: temporaryPassword,
			ForceLogout:       forceLogout,
		}
		sessionClearFailed := false
		if forceLogout {
			clearedSessions := true
			resp.ClearedSessions = &clearedSessions
			if kratosManaged {
				if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), kratosIdentityID); err != nil {
					clearedSessions = false
					resp.ClearedSessions = &clearedSessions
					sessionClearFailed = true
				}
			}
			if err := harukiAPIHelper.ClearUserSessions(apiHelper.RedisClient(), targetUser.ID); err != nil {
				clearedSessions = false
				resp.ClearedSessions = &clearedSessions
				sessionClearFailed = true
			}
		}

		metadata := map[string]any{
			"generatedTemporaryPassword": temporaryPassword != "",
			"forceLogout":                forceLogout,
			"localMirrorFailed":          localMirrorFailed,
		}
		if localMirrorFailureReason != "" {
			metadata["localMirrorFailReason"] = localMirrorFailureReason
		}
		if resp.ClearedSessions != nil {
			metadata["clearedSessions"] = *resp.ClearedSessions
			metadata["sessionClearFailed"] = sessionClearFailed
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		if localMirrorFailed && sessionClearFailed {
			return harukiAPIHelper.SuccessResponse(c, "password reset, but local mirror sync failed and some sessions were not cleared", &resp)
		}
		if localMirrorFailed {
			return harukiAPIHelper.SuccessResponse(c, "password reset, but local mirror sync failed", &resp)
		}
		if sessionClearFailed {
			return harukiAPIHelper.SuccessResponse(c, "password reset, but failed to clear user sessions", &resp)
		}
		return harukiAPIHelper.SuccessResponse(c, "password reset", &resp)
	}
}
