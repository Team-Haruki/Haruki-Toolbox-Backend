package user

import (
	"bytes"
	"encoding/base64"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"haruki-suite/config"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	_ "golang.org/x/image/webp"
)

func handleUpdateProfile(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID := c.Locals("userID").(string)
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		updatedName := false
		updatedAvatar := false
		defer func() {
			writeUserAuditLog(c, apiHelper, "user.profile.update", result, userID, map[string]any{
				"reason":        reason,
				"updatedName":   updatedName,
				"updatedAvatar": updatedAvatar,
			})
		}()

		var payload harukiAPIHelper.UpdateProfilePayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}
		ctx := c.Context()
		ub := apiHelper.DBManager.DB.User.Update().Where(user.IDEQ(userID))
		var avatarFileName string
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
			if _, _, err := image.Decode(bytes.NewReader(decodedAvatar)); err != nil {
				harukiLogger.Warnf("Invalid image data from user %s: %v", userID, err)
				reason = "invalid_avatar_image"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid or corrupted image data", nil)
			}
			avatarFileName = uuid.NewString() + ext
			savePath := filepath.Join(config.Cfg.UserSystem.AvatarSaveDir, filepath.Base(avatarFileName))
			if err := os.WriteFile(savePath, decodedAvatar, 0644); err != nil {
				harukiLogger.Errorf("Failed to save avatar file: %v", err)
				reason = "save_avatar_failed"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to save avatar", nil)
			}
			ub = ub.SetAvatarPath(avatarFileName)
			updatedAvatar = true
		}
		if payload.Name != nil {
			name := strings.TrimSpace(*payload.Name)
			if len(name) == 0 || len(name) > 50 {
				reason = "invalid_name"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Name must be 1-50 characters", nil)
			}
			ub = ub.SetName(name)
			updatedName = true
		}
		_, err := ub.Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update user profile: %v", err)
			reason = "update_profile_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to update profile", nil)
		}
		ud := harukiAPIHelper.HarukiToolboxUserData{}
		if payload.Name != nil {
			ud.Name = payload.Name
		}
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
		userID := c.Locals("userID").(string)
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			writeUserAuditLog(c, apiHelper, "user.password.change", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		var payload harukiAPIHelper.ChangePasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}
		ctx := c.Context()
		u, err := apiHelper.DBManager.DB.User.Query().Where(user.IDEQ(userID)).Only(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query user %s: %v", userID, err)
			reason = "query_user_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to verify user", nil)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(payload.OldPassword)); err != nil {
			reason = "old_password_invalid"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Old password is incorrect", nil)
		}
		if len(payload.NewPassword) < 8 {
			reason = "new_password_too_short"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "password must be at least 8 characters", nil)
		}
		if len([]byte(payload.NewPassword)) > 72 {
			reason = "new_password_too_long"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "password is too long (max 72 bytes)", nil)
		}
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(payload.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash password: %v", err)
			reason = "hash_password_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to process request", nil)
		}
		_, err = apiHelper.DBManager.DB.User.
			Update().Where(user.IDEQ(userID)).
			SetPasswordHash(string(hashedPassword)).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update password: %v", err)
			reason = "update_password_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to update password", nil)
		}
		if err := harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, userID); err != nil {
			harukiLogger.Errorf("Failed to clear user sessions: %v", err)
			reason = "clear_sessions_failed"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to clear sessions", nil)
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated", nil)
	}
}

func registerAccountRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id")

	r.Put("/profile", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleUpdateProfile(apiHelper))
	r.Put("/change-password", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleChangePassword(apiHelper))

}
