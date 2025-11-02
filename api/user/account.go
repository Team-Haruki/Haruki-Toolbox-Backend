package user

import (
	"context"
	"encoding/base64"
	"fmt"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/user"
	"os"
	"path/filepath"
	"strings"

	"haruki-suite/config"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func handleUpdateProfile(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var payload harukiAPIHelper.UpdateProfilePayload
		if err := c.BodyParser(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}

		ctx := context.Background()
		userID := c.Locals("userID").(string)
		ub := apiHelper.DBManager.DB.User.Update().Where(user.IDEQ(userID))

		var avatarFileName string
		if payload.AvatarBase64 != nil {
			base64Data := *payload.AvatarBase64
			ext := ".png"
			if strings.Contains(base64Data, ";base64,") {
				parts := strings.SplitN(base64Data, ";base64,", 2)
				mimeType := parts[0]
				base64Data = parts[1]
				switch mimeType {
				case "data:image/png":
					ext = ".png"
				case "data:image/jpeg":
					ext = ".jpg"
				default:
					ext = ".png"
				}
			}

			decodedAvatar, err := base64.StdEncoding.DecodeString(base64Data)
			if err != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid base64 avatar data", nil)
			}

			avatarFileName = uuid.NewString() + ext
			savePath := filepath.Join(config.Cfg.UserSystem.AvatarSaveDir, avatarFileName)
			if err := os.WriteFile(savePath, decodedAvatar, 0644); err != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Failed to save avatar", nil)
			}
			ub = ub.SetAvatarPath(avatarFileName)
		}

		if payload.Name != nil {
			ub = ub.SetName(*payload.Name)
		}

		_, err := ub.Save(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Failed to update user profile", nil)
		}

		ud := harukiAPIHelper.HarukiToolboxUserData{}
		if payload.Name != nil {
			ud.Name = payload.Name
		}
		if payload.AvatarBase64 != nil {
			url := fmt.Sprintf("%s/avatars/%s", strings.TrimRight(config.Cfg.UserSystem.FrontendURL, "/"), avatarFileName)
			ud.AvatarPath = &url
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "profile updated", &ud)
	}
}

func handleChangePassword(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var payload harukiAPIHelper.ChangePasswordPayload
		if err := c.BodyParser(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}

		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Failed to hash password", nil)
		}

		ctx := context.Background()
		userID := c.Locals("userID").(string)

		_, err = apiHelper.DBManager.DB.User.
			Update().Where(user.IDEQ(userID)).
			SetPasswordHash(string(hashedPassword)).
			Save(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Failed to update password", nil)
		}
		_ = harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, userID)
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated", nil)
	}
}

func registerAccountRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id")

	r.Put("/profile", apiHelper.SessionHandler.VerifySessionToken, handleUpdateProfile(apiHelper))
	r.Put("/change-password", apiHelper.SessionHandler.VerifySessionToken, handleChangePassword(apiHelper))

}
