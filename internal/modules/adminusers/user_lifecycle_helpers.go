package adminusers

import (
	"crypto/rand"
	"encoding/hex"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
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
