package user

import (
	"context"
	"encoding/base64"
	"haruki-suite/utils/database/postgresql/user"
	"net/http"
	"os"
	"path/filepath"

	"haruki-suite/config"
	"haruki-suite/utils/database/postgresql"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func RegisterUpdateProfileRoute(router fiber.Router, postgresqlClient *postgresql.Client) {
	r := router.Group("/api/user", VerifySessionToken)

	r.Put("/:toolbox_user_id/profile", func(c *fiber.Ctx) error {
		var payload UpdateProfilePayload
		if err := c.BodyParser(&payload); err != nil {
			return UpdatedDataResponse[string](c, http.StatusBadRequest, "Invalid request payload", nil)
		}

		decodedAvatar, err := base64.StdEncoding.DecodeString(payload.AvatarBase64)
		if err != nil {
			return UpdatedDataResponse[string](c, http.StatusBadRequest, "Invalid base64 avatar data", nil)
		}

		filename := uuid.NewString() + ".png"
		avatarPath := filepath.Join(config.Cfg.UserSystem.AvatarSaveDir, filename)
		if err := os.WriteFile(avatarPath, decodedAvatar, 0644); err != nil {
			return UpdatedDataResponse[string](c, http.StatusBadRequest, "Failed to save avatar", nil)
		}

		ctx := context.Background()
		userID := c.Params("toolbox_user_id")
		_, err = postgresqlClient.User.
			Update().Where(user.UserIDEQ(userID)).
			SetName(payload.Name).
			SetAvatarPath(avatarPath).
			Save(ctx)
		if err != nil {
			return UpdatedDataResponse[string](c, http.StatusBadRequest, "Failed to update user profile", nil)
		}

		ud := UserData{Name: payload.Name, AvatarPath: &avatarPath}
		return UpdatedDataResponse(c, http.StatusOK, "profile updated", &ud)
	})
}
