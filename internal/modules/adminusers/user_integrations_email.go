package adminusers

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	adminLocalMirrorRetryAttempts = 3
	adminLocalMirrorRetryInterval = 150 * time.Millisecond
)

func resolveAdminUserEmailUpdateFinalizeOutcome(localMirrorFailed, sessionClearFailed bool) (status int, message string, auditResult string) {
	if localMirrorFailed && sessionClearFailed {
		return fiber.StatusInternalServerError, "user email updated in identity provider, but local mirror sync failed and some sessions were not cleared", harukiAPIHelper.SystemLogResultFailure
	}
	if localMirrorFailed {
		return fiber.StatusInternalServerError, "user email updated in identity provider, but local mirror sync failed", harukiAPIHelper.SystemLogResultFailure
	}
	if sessionClearFailed {
		return fiber.StatusOK, "user email updated, but failed to clear user sessions", harukiAPIHelper.SystemLogResultSuccess
	}
	return fiber.StatusOK, "user email updated", harukiAPIHelper.SystemLogResultSuccess
}

func handleUpdateUserEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserEmailUpdate
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		payload, err := parseAdminManagedEmailPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		userConflict, err := apiHelper.DBManager.DB.User.Query().
			Where(
				userSchema.EmailEqualFold(payload.Email),
				userSchema.IDNEQ(targetUser.ID),
			).
			Exist(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryUserConflictFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to check email conflict")
		}
		if userConflict {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonEmailConflict, nil))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "email already in use", nil)
		}
		kratosUpdated := false
		localMirrorFailed := false
		localMirrorFailReason := ""
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() &&
			targetUser.KratosIdentityID != nil && strings.TrimSpace(*targetUser.KratosIdentityID) != "" {
			if err := apiHelper.SessionHandler.UpdateKratosEmailByIdentityID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), strings.TrimSpace(*targetUser.KratosIdentityID), payload.Email); err != nil {
				switch {
				case harukiAPIHelper.IsKratosIdentityConflictError(err):
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonEmailConflict, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "email already in use", nil)
				case harukiAPIHelper.IsKratosIdentityUnmappedError(err):
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorNotFound(c, "user identity not found")
				case harukiAPIHelper.IsIdentityProviderUnavailableError(err):
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonIdentityProviderUnavailable, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorInternal(c, "identity provider unavailable")
				default:
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateUserEmailFailed, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorInternal(c, "failed to update user email")
				}
			}
			kratosUpdated = true
		}

		affected := 0
		err = harukiAPIHelper.RetryOperation(c.Context(), adminLocalMirrorRetryAttempts, adminLocalMirrorRetryInterval, func() error {
			nextAffected, updateErr := applyManagedTargetUserUpdateGuards(
				apiHelper.DBManager.DB.User.Update().SetEmail(payload.Email),
				actorUserID,
				actorRole,
				targetUser.ID,
			).Save(c.Context())
			if updateErr == nil {
				affected = nextAffected
			}
			return updateErr
		})
		if err != nil {
			if !kratosUpdated {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateUserEmailFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update user email")
			}
			localMirrorFailed = true
			localMirrorFailReason = "update_user_email_failed"
		}
		if err == nil && affected == 0 {
			if !kratosUpdated {
				missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
					"guardedUpdateMiss": true,
				}))
				return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to update user email")
			}
			localMirrorFailed = true
			localMirrorFailReason = "guarded_update_miss"
		}

		resp := adminUserEmailResponse{
			UserID:   targetUser.ID,
			Email:    payload.Email,
			Verified: false,
		}
		sessionClearFailed := clearManagedUserSessions(c.Context(), apiHelper, targetUser.ID, targetUser.KratosIdentityID)

		metadata := map[string]any{
			"email":              payload.Email,
			"verified":           true,
			"localMirrorFailed":  localMirrorFailed,
			"sessionClearFailed": sessionClearFailed,
		}
		if localMirrorFailReason != "" {
			metadata["localMirrorFailReason"] = localMirrorFailReason
		}

		status, message, auditResult := resolveAdminUserEmailUpdateFinalizeOutcome(localMirrorFailed, sessionClearFailed)
		if auditResult == harukiAPIHelper.SystemLogResultSuccess {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, auditResult, metadata)
		} else {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, auditResult, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateUserEmailFailed, metadata))
		}
		if status == fiber.StatusOK {
			return harukiAPIHelper.SuccessResponse(c, message, &resp)
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, status, message, nil)
	}
}
