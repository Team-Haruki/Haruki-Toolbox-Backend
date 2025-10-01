package user

import (
	"context"
	"encoding/base64"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/user"
	"os"
	"path/filepath"

	"haruki-suite/config"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func registerAccountRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id")

	r.Put("/profile", apiHelper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		var payload harukiAPIHelper.UpdateProfilePayload
		if err := c.BodyParser(&payload); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid request payload", nil)
		}

		ctx := context.Background()
		userID := c.Locals("userID").(string)
		ub := apiHelper.DBManager.DB.User.Update().Where(user.IDEQ(userID))

		var avatarPath string
		if payload.AvatarBase64 != nil {
			decodedAvatar, err := base64.StdEncoding.DecodeString(*payload.AvatarBase64)
			if err != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid base64 avatar data", nil)
			}

			filename := uuid.NewString() + ".png"
			avatarPath = filepath.Join(config.Cfg.UserSystem.AvatarSaveDir, filename)
			if err := os.WriteFile(avatarPath, decodedAvatar, 0644); err != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Failed to save avatar", nil)
			}
			ub = ub.SetAvatarPath(avatarPath)
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
			ud.AvatarPath = &avatarPath
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "profile updated", &ud)
	})

	r.Put("/change-password", apiHelper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
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
		harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, userID)
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "password updated", nil)
	})

}
