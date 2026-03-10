package adminusers

import (
	"crypto/rand"
	"encoding/hex"
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

const (
	adminLocalPasswordMirrorRetryAttempts = 3
	adminLocalPasswordMirrorRetryInterval = 150 * time.Millisecond
)

func buildSoftDeleteBanReason(reason *string) string {
	if reason == nil {
		return softDeleteBanReasonPrefix
	}
	trimmed := strings.TrimSpace(*reason)
	if trimmed == "" {
		return softDeleteBanReasonPrefix
	}
	return softDeleteBanReasonPrefix + " " + trimmed
}

func generateTemporaryPassword() (string, error) {
	b := make([]byte, temporaryPasswordBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return temporaryPasswordPrefix + hex.EncodeToString(b), nil
}

func validateAdminPasswordInput(raw string) error {
	if len(raw) < adminPasswordMinLengthChars {
		return fiber.NewError(fiber.StatusBadRequest, "password must be at least 8 characters")
	}
	if len([]byte(raw)) > adminPasswordMaxLengthBytes {
		return fiber.NewError(fiber.StatusBadRequest, "password is too long (max 72 bytes)")
	}
	return nil
}

func queryAdminTargetUser(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string) (*postgresql.User, error) {
	targetUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(targetUserID)).
		Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned, userSchema.FieldBanReason, userSchema.FieldKratosIdentityID).
		Only(c.Context())
	if err != nil {
		return nil, err
	}
	return targetUser, nil
}

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

func handleRestoreUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		targetUser, err := queryAdminTargetUser(c, apiHelper, targetUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		affected, err := applyManagedTargetUserUpdateGuards(
			apiHelper.DBManager.DB.User.Update().
				SetBanned(false).
				ClearBanReason(),
			actorUserID,
			actorRole,
			targetUser.ID,
		).Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to restore user")
		}
		if affected == 0 {
			missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to restore user")
		}

		updated, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUser.ID)).
			Select(userSchema.FieldID, userSchema.FieldBanned, userSchema.FieldBanReason).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		resp := adminLifecycleResponse{
			UserID:    updated.ID,
			Banned:    updated.Banned,
			BanReason: updated.BanReason,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserRestore, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "user restored", &resp)
	}
}

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
			if err := apiHelper.SessionHandler.UpdateKratosPasswordByIdentityID(c.Context(), kratosIdentityID, password); err != nil {
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
		hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			if !kratosManaged {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonHashPasswordFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to hash password")
			}
			localMirrorFailed = true
			localMirrorFailureReason = "hash_password_failed"
		} else {
			affected := 0
			err := harukiAPIHelper.RetryOperation(c.Context(), adminLocalPasswordMirrorRetryAttempts, adminLocalPasswordMirrorRetryInterval, func() error {
				nextAffected, updateErr := applyManagedTargetUserUpdateGuards(
					apiHelper.DBManager.DB.User.Update().SetPasswordHash(string(hashed)),
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
				if !kratosManaged {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdatePasswordFailed, nil))
					return harukiAPIHelper.ErrorInternal(c, "failed to reset password")
				}
				localMirrorFailed = true
				localMirrorFailureReason = "update_password_failed"
			}
			if err == nil && affected == 0 {
				if !kratosManaged {
					missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserResetPass, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
						"guardedUpdateMiss": true,
					}))
					return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to reset password")
				}
				localMirrorFailed = true
				localMirrorFailureReason = "guarded_update_miss"
			}
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
				if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(c.Context(), kratosIdentityID); err != nil {
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
