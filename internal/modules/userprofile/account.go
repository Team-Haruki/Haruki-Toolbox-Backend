package userprofile

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"haruki-suite/config"
	userModule "haruki-suite/internal/modules/user"
	userauth "haruki-suite/internal/modules/userauth"
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

const (
	localMirrorRetryAttempts = 3
	localMirrorRetryInterval = 150 * time.Millisecond
	maxAvatarPixels          = int64(4096 * 4096)
)

func ensureAvatarSaveDir(baseDir string) error {
	trimmed := strings.TrimSpace(baseDir)
	if trimmed == "" {
		return fmt.Errorf("avatar save dir is empty")
	}
	return os.MkdirAll(trimmed, 0755)
}

func buildAvatarFilePath(baseDir, avatarFileName string) string {
	return filepath.Join(baseDir, filepath.Base(strings.TrimSpace(avatarFileName)))
}

func removeAvatarFileIfExists(filePath string) error {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return nil
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func hasProfileUpdatePayload(payload harukiAPIHelper.UpdateProfilePayload) bool {
	return payload.AvatarBase64 != nil
}

func handleUpdateProfile(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		updatedAvatar := false
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.profile.update", result, userID, map[string]any{
				"reason":        reason,
				"updatedName":   false,
				"updatedAvatar": updatedAvatar,
			})
		}()

		var payload harukiAPIHelper.UpdateProfilePayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}
		if !hasProfileUpdatePayload(payload) {
			reason = "empty_payload"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "No profile fields to update", nil)
		}

		ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
		currentUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Select(userSchema.FieldID, userSchema.FieldAvatarPath).
			Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				reason = "user_not_found"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
			}
			harukiLogger.Errorf("Failed to query current user profile: %v", err)
			reason = "query_user_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to update profile", nil)
		}
		oldAvatarPath := ""
		if currentUser.AvatarPath != nil {
			oldAvatarPath = strings.TrimSpace(*currentUser.AvatarPath)
		}

		ub := apiHelper.DBManager.DB.User.Update().Where(userSchema.IDEQ(userID))
		var avatarFileName string
		var newAvatarSavePath string
		if payload.AvatarBase64 != nil {
			base64Data := *payload.AvatarBase64
			if strings.Contains(base64Data, ";base64,") {
				parts := strings.SplitN(base64Data, ";base64,", 2)
				base64Data = parts[1]
			}
			decodedAvatar, err := base64.StdEncoding.DecodeString(base64Data)
			if err != nil {
				reason = "invalid_avatar_base64"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid base64 avatar data", nil)
			}

			if len(decodedAvatar) > 2*1024*1024 {
				reason = "avatar_too_large"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Avatar image is too large (max 2MB)", nil)
			}
			detectedMIME := http.DetectContentType(decodedAvatar)
			allowedMIMEs := map[string]string{
				"image/png":  ".png",
				"image/jpeg": ".jpg",
				"image/gif":  ".gif",
				"image/webp": ".webp",
			}
			ext, ok := allowedMIMEs[detectedMIME]
			if !ok {
				reason = "unsupported_avatar_format"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Unsupported image format. Allowed: PNG, JPEG, GIF, WebP", nil)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(decodedAvatar))
			if err != nil {
				harukiLogger.Warnf("Invalid image data from user %s: %v", userID, err)
				reason = "invalid_avatar_image"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid or corrupted image data", nil)
			}
			if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxAvatarPixels {
				reason = "avatar_too_large"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Avatar image dimensions are too large", nil)
			}
			avatarFileName = uuid.NewString() + ext
			if err := ensureAvatarSaveDir(config.Cfg.UserSystem.AvatarSaveDir); err != nil {
				harukiLogger.Errorf("Failed to prepare avatar directory: %v", err)
				reason = "save_avatar_failed"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to save avatar", nil)
			}
			savePath := buildAvatarFilePath(config.Cfg.UserSystem.AvatarSaveDir, avatarFileName)
			if err := os.WriteFile(savePath, decodedAvatar, 0644); err != nil {
				harukiLogger.Errorf("Failed to save avatar file: %v", err)
				reason = "save_avatar_failed"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to save avatar", nil)
			}
			newAvatarSavePath = savePath
			ub = ub.SetAvatarPath(avatarFileName)
			updatedAvatar = true
		}
		_, err = ub.Save(ctx)
		if err != nil {
			if newAvatarSavePath != "" {
				if cleanupErr := removeAvatarFileIfExists(newAvatarSavePath); cleanupErr != nil {
					harukiLogger.Warnf("Failed to cleanup avatar file after profile update failure: %v", cleanupErr)
				}
			}
			harukiLogger.Errorf("Failed to update user profile: %v", err)
			reason = "update_profile_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to update profile", nil)
		}
		if updatedAvatar && oldAvatarPath != "" && oldAvatarPath != avatarFileName {
			oldAvatarFullPath := buildAvatarFilePath(config.Cfg.UserSystem.AvatarSaveDir, oldAvatarPath)
			if err := removeAvatarFileIfExists(oldAvatarFullPath); err != nil {
				harukiLogger.Warnf("Failed to cleanup old avatar file for user %s: %v", userID, err)
			}
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{}
		if payload.AvatarBase64 != nil {
			url := fmt.Sprintf("%s/avatars/%s", strings.TrimRight(config.Cfg.UserSystem.AvatarURL, "/"), avatarFileName)
			ud.AvatarPath = &url
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "profile updated", &ud)
	}
}

func handleChangePassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		sessionClearFailed := false
		localMirrorFailed := false
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.password.change", result, userID, map[string]any{
				"reason":             reason,
				"sessionClearFailed": sessionClearFailed,
				"localMirrorFailed":  localMirrorFailed,
			})
		}()

		var payload harukiAPIHelper.ChangePasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}
		ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
		u, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(userID)).Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				reason = "user_not_found"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
			}
			harukiLogger.Errorf("Failed to query user %s: %v", userID, err)
			reason = "query_user_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to verify user", nil)
		}
		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() {
			return handleChangePasswordViaKratos(c, apiHelper, u, payload, &result, &reason, &sessionClearFailed, &localMirrorFailed)
		}
		reason = "managed_identity_required"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusGone, userauth.ManagedIdentityMessage, nil)
	}
}

func handleChangePasswordViaKratos(
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	user *postgresql.User,
	payload harukiAPIHelper.ChangePasswordPayload,
	result *string,
	reason *string,
	sessionClearFailed *bool,
	localMirrorFailed *bool,
) error {
	ctx := harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	if user == nil {
		*reason = "invalid_user"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
	}
	if userModule.IsPasswordTooShort(payload.NewPassword) {
		*reason = "new_password_too_short"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, userModule.PasswordTooShortMessage, nil)
	}
	if userModule.IsPasswordTooLong(payload.NewPassword) {
		*reason = "new_password_too_long"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, userModule.PasswordTooLongMessage, nil)
	}
	if user.KratosIdentityID == nil || strings.TrimSpace(*user.KratosIdentityID) == "" {
		*reason = "identity_not_linked"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
	}
	kratosIdentityID := strings.TrimSpace(*user.KratosIdentityID)

	err := apiHelper.SessionHandler.VerifyKratosPasswordByIdentityID(ctx, kratosIdentityID, payload.OldPassword)
	if err != nil {
		if harukiAPIHelper.IsKratosInvalidCredentialsError(err) || harukiAPIHelper.IsKratosInvalidInputError(err) {
			*reason = "old_password_invalid"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Old password is incorrect", nil)
		}
		if harukiAPIHelper.IsKratosIdentityUnmappedError(err) {
			*reason = "identity_not_found"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
		}
		if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
			*reason = "identity_provider_unavailable"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to process request", nil)
		}
		harukiLogger.Errorf("Kratos old password verification failed for user %s: %v", user.ID, err)
		*reason = "verify_old_password_failed"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to process request", nil)
	}

	if err := apiHelper.SessionHandler.UpdateKratosPasswordByIdentityID(ctx, kratosIdentityID, payload.NewPassword); err != nil {
		if harukiAPIHelper.IsKratosIdentityUnmappedError(err) {
			*reason = "identity_not_found"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
		}
		if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
			*reason = "identity_provider_unavailable"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to process request", nil)
		}
		harukiLogger.Errorf("Kratos password update failed for user %s: %v", user.ID, err)
		*reason = "update_kratos_password_failed"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to update password", nil)
	}

	_ = localMirrorFailed

	if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(ctx, kratosIdentityID); err != nil {
		harukiLogger.Warnf("Failed to revoke Kratos sessions for user %s: %v", user.ID, err)
		*sessionClearFailed = true
	}
	if err := harukiAPIHelper.ClearUserSessions(apiHelper.RedisClient(), user.ID); err != nil {
		harukiLogger.Warnf("Failed to clear local user sessions: %v", err)
		*sessionClearFailed = true
	}

	if *localMirrorFailed && *sessionClearFailed {
		*result = harukiAPIHelper.SystemLogResultSuccess
		*reason = "ok_local_mirror_and_session_clear_failed"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated, but failed to sync local mirror and clear some sessions", nil)
	}
	if *localMirrorFailed {
		*result = harukiAPIHelper.SystemLogResultSuccess
		*reason = "ok_local_mirror_failed"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated, but local mirror sync failed", nil)
	}
	if *sessionClearFailed {
		*result = harukiAPIHelper.SystemLogResultSuccess
		*reason = "ok_session_clear_failed"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated, but failed to clear existing sessions", nil)
	}
	*result = harukiAPIHelper.SystemLogResultSuccess
	*reason = "ok"
	return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated", nil)
}

func RegisterUserProfileRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id")

	r.Put("/profile", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleUpdateProfile(apiHelper))
	if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesManagedBrowserAuth() {
		r.Put("/change-password", userauth.LegacyAuthDisabledHandler())
		return
	}
	r.Put("/change-password", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleChangePassword(apiHelper))
}
