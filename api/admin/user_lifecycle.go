package admin

import (
	"crypto/rand"
	"encoding/hex"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

const softDeleteBanReasonPrefix = "[soft_deleted]"

type adminSoftDeletePayload struct {
	Reason *string `json:"reason,omitempty"`
}

type adminResetPasswordPayload struct {
	NewPassword *string `json:"newPassword,omitempty"`
	ForceLogout *bool   `json:"forceLogout,omitempty"`
}

type adminLifecycleResponse struct {
	UserID    string  `json:"userId"`
	Banned    bool    `json:"banned"`
	BanReason *string `json:"banReason,omitempty"`
}

type adminResetPasswordResponse struct {
	UserID            string `json:"userId"`
	TemporaryPassword string `json:"temporaryPassword,omitempty"`
	ForceLogout       bool   `json:"forceLogout"`
}

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
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "Tmp-" + hex.EncodeToString(b), nil
}

func validateAdminPasswordInput(raw string) error {
	if len(raw) < 8 {
		return fiber.NewError(fiber.StatusBadRequest, "password must be at least 8 characters")
	}
	if len([]byte(raw)) > 72 {
		return fiber.NewError(fiber.StatusBadRequest, "password is too long (max 72 bytes)")
	}
	return nil
}

func queryAdminTargetUser(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string) (*postgresql.User, error) {
	targetUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(targetUserID)).
		Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned, userSchema.FieldBanReason).
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
			writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload adminSoftDeletePayload
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&payload); err != nil {
				writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
			}
		}
		reason, err := sanitizeBanReason(payload.Reason)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_reason", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid reason")
		}

		targetUser, err := queryAdminTargetUser(c, apiHelper, targetUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		banReason := buildSoftDeleteBanReason(reason)
		updated, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUser.ID).
			SetBanned(true).
			SetBanReason(banReason).
			Save(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to soft delete user")
		}
		_ = harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, targetUser.ID)

		resp := adminLifecycleResponse{
			UserID:    updated.ID,
			Banned:    updated.Banned,
			BanReason: updated.BanReason,
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.soft_delete", "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"hasReason": reason != nil,
		})
		return harukiAPIHelper.SuccessResponse(c, "user soft deleted", &resp)
	}
}

func handleRestoreUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.restore", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.restore", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		targetUser, err := queryAdminTargetUser(c, apiHelper, targetUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.restore", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.restore", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.restore", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		updated, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUser.ID).
			SetBanned(false).
			ClearBanReason().
			Save(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.restore", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to restore user")
		}

		resp := adminLifecycleResponse{
			UserID:    updated.ID,
			Banned:    updated.Banned,
			BanReason: updated.BanReason,
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.restore", "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "user restored", &resp)
	}
}

func handleResetUserPassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload adminResetPasswordPayload
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&payload); err != nil {
				writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
			}
		}

		targetUser, err := queryAdminTargetUser(c, apiHelper, targetUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		password := ""
		temporaryPassword := ""
		if payload.NewPassword != nil {
			password = *payload.NewPassword
		} else {
			generated, err := generateTemporaryPassword()
			if err != nil {
				writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("generate_temporary_password_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to generate temporary password")
			}
			password = generated
			temporaryPassword = generated
		}

		if err := validateAdminPasswordInput(password); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_password", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid password")
		}

		hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("hash_password_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to hash password")
		}

		if _, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUser.ID).SetPasswordHash(string(hashed)).Save(c.Context()); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_password_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to reset password")
		}

		forceLogout := true
		if payload.ForceLogout != nil {
			forceLogout = *payload.ForceLogout
		}
		if forceLogout {
			_ = harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, targetUser.ID)
		}

		resp := adminResetPasswordResponse{
			UserID:            targetUser.ID,
			TemporaryPassword: temporaryPassword,
			ForceLogout:       forceLogout,
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.reset_password", "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"generatedTemporaryPassword": temporaryPassword != "",
			"forceLogout":                forceLogout,
		})
		return harukiAPIHelper.SuccessResponse(c, "password reset", &resp)
	}
}
