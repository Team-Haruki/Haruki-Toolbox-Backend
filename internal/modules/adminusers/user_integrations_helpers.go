package adminusers

import (
	"crypto/rand"
	"encoding/hex"
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"net/mail"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func normalizeAdminManageSocialPlatform(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(harukiAPIHelper.SocialPlatformQQ):
		return string(harukiAPIHelper.SocialPlatformQQ), nil
	case string(harukiAPIHelper.SocialPlatformQQBot):
		return string(harukiAPIHelper.SocialPlatformQQBot), nil
	case string(harukiAPIHelper.SocialPlatformDiscord):
		return string(harukiAPIHelper.SocialPlatformDiscord), nil
	case string(harukiAPIHelper.SocialPlatformTelegram):
		return string(harukiAPIHelper.SocialPlatformTelegram), nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "unsupported social platform")
	}
}

func parseAdminManagedSocialPlatformPayload(c fiber.Ctx) (*adminManagedSocialPlatformPayload, error) {
	var payload adminManagedSocialPlatformPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	platform := strings.TrimSpace(payload.Platform)
	if platform == "" {
		platform = strings.TrimSpace(payload.PlatformSnake)
	}
	normalizedPlatform, err := normalizeAdminManageSocialPlatform(platform)
	if err != nil {
		return nil, err
	}

	userID := strings.TrimSpace(payload.UserID)
	if userID == "" {
		userID = strings.TrimSpace(payload.UserIDSnake)
	}
	if userID == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userId is required")
	}

	verified := true
	if payload.Verified != nil {
		verified = *payload.Verified
	}

	return &adminManagedSocialPlatformPayload{
		Platform: normalizedPlatform,
		UserID:   userID,
		Verified: &verified,
	}, nil
}

func parseAdminManagedAuthorizedSocialPayload(c fiber.Ctx) (*adminManagedAuthorizedSocialPayload, error) {
	var payload adminManagedAuthorizedSocialPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	platform := strings.TrimSpace(payload.Platform)
	if platform == "" {
		platform = strings.TrimSpace(payload.PlatformSnake)
	}
	normalizedPlatform, err := normalizeAdminManageSocialPlatform(platform)
	if err != nil {
		return nil, err
	}

	userID := strings.TrimSpace(payload.UserID)
	if userID == "" {
		userID = strings.TrimSpace(payload.UserIDSnake)
	}
	if userID == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userId is required")
	}

	return &adminManagedAuthorizedSocialPayload{
		Platform: normalizedPlatform,
		UserID:   userID,
		Comment:  strings.TrimSpace(payload.Comment),
	}, nil
}

func parseAdminGameBindingPayload(c fiber.Ctx) (*harukiAPIHelper.CreateGameAccountBindingPayload, error) {
	var payload harukiAPIHelper.CreateGameAccountBindingPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}
	return &payload, nil
}

func parseAdminManagedEmailPayload(c fiber.Ctx) (*adminManagedEmailPayload, error) {
	var payload adminManagedEmailPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	payload.Email = platformIdentity.NormalizeEmail(payload.Email)
	if payload.Email == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "email is required")
	}
	if len(payload.Email) > 320 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "email exceeds max length")
	}

	parsed, err := mail.ParseAddress(payload.Email)
	if err != nil || parsed == nil || platformIdentity.NormalizeEmail(parsed.Address) != payload.Email {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid email format")
	}

	return &payload, nil
}

func parseAdminUpdateAllowCNMysekaiPayload(c fiber.Ctx) (*adminUpdateAllowCNMysekaiPayload, error) {
	var payload adminUpdateAllowCNMysekaiPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	allow := payload.AllowCNMysekai
	if allow == nil {
		allow = payload.AllowCNMysekaiSnake
	}
	if allow == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "allowCNMysekai is required")
	}

	return &adminUpdateAllowCNMysekaiPayload{
		AllowCNMysekai: allow,
	}, nil
}

func resolveManageableTargetUser(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string) (*postgresql.User, error) {
	targetUserID := strings.TrimSpace(c.Params("target_user_id"))
	if targetUserID == "" {
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
		return nil, fiber.NewError(fiber.StatusBadRequest, "target_user_id is required")
	}

	actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
	if err != nil {
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
		return nil, err
	}

	targetUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(targetUserID)).
		Select(userSchema.FieldID, userSchema.FieldRole).
		Only(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
			return nil, fiber.NewError(fiber.StatusNotFound, "user not found")
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to query target user")
	}

	if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
			"actorRole":  actorRole,
			"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
		}))
		return nil, err
	}

	return targetUser, nil
}

func buildAdminGameBindingItems(rows []*postgresql.GameAccountBinding) []harukiAPIHelper.GameAccountBinding {
	items := make([]harukiAPIHelper.GameAccountBinding, 0, len(rows))
	for _, row := range rows {
		items = append(items, harukiAPIHelper.GameAccountBinding{
			Server:   harukiUtils.SupportedDataUploadServer(row.Server),
			UserID:   row.GameUserID,
			Verified: row.Verified,
			Suite:    row.Suite,
			Mysekai:  row.Mysekai,
		})
	}
	return items
}

func buildAdminAuthorizedSocialItems(rows []*postgresql.AuthorizeSocialPlatformInfo) []harukiAPIHelper.AuthorizeSocialPlatformInfo {
	items := make([]harukiAPIHelper.AuthorizeSocialPlatformInfo, 0, len(rows))
	for _, row := range rows {
		items = append(items, harukiAPIHelper.AuthorizeSocialPlatformInfo{
			ID:       row.PlatformID,
			Platform: row.Platform,
			UserID:   row.PlatformUserID,
			Comment:  row.Comment,
		})
	}
	return items
}

func generateAdminIOSUploadCode() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
