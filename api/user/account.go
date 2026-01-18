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
		var payload harukiAPIHelper.UpdateProfilePayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}
		ctx := c.Context()
		userID := c.Locals("userID").(string)
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
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid base64 avatar data", nil)
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
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Unsupported image format. Allowed: PNG, JPEG, GIF, WebP", nil)
			}
			if _, _, err := image.Decode(bytes.NewReader(decodedAvatar)); err != nil {
				harukiLogger.Warnf("Invalid image data from user %s: %v", userID, err)
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid or corrupted image data", nil)
			}
			avatarFileName = uuid.NewString() + ext
			savePath := filepath.Join(config.Cfg.UserSystem.AvatarSaveDir, filepath.Base(avatarFileName))
			if err := os.WriteFile(savePath, decodedAvatar, 0644); err != nil {
				harukiLogger.Errorf("Failed to save avatar file: %v", err)
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to save avatar", nil)
			}
			ub = ub.SetAvatarPath(avatarFileName)
		}
		if payload.Name != nil {
			ub = ub.SetName(*payload.Name)
		}
		_, err := ub.Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update user profile: %v", err)
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
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "profile updated", &ud)
	}
}

func handleChangePassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload harukiAPIHelper.ChangePasswordPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		u, err := apiHelper.DBManager.DB.User.Query().Where(user.IDEQ(userID)).Only(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query user %s: %v", userID, err)
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to verify user", nil)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(payload.OldPassword)); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Old password is incorrect", nil)
		}
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(payload.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			harukiLogger.Errorf("Failed to hash password: %v", err)
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to process request", nil)
		}
		_, err = apiHelper.DBManager.DB.User.
			Update().Where(user.IDEQ(userID)).
			SetPasswordHash(string(hashedPassword)).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to update password: %v", err)
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to update password", nil)
		}
		if err := harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, userID); err != nil {
			harukiLogger.Errorf("Failed to clear user sessions: %v", err)
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated", nil)
	}
}

func registerAccountRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id")

	r.Put("/profile", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleUpdateProfile(apiHelper))
	r.Put("/change-password", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleChangePassword(apiHelper))

}
