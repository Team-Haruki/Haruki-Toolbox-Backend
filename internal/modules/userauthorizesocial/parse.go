package userauthorizesocial

import (
	"strconv"
	"strings"
	"unicode/utf8"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"

	"github.com/gofiber/fiber/v3"
)

const maxUserSocialGrants = 128

func parseAuthorizeSocialPlatformID(c fiber.Ctx) (int, error) {
	idParam := c.Params("id")
	userAccountID64, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return 0, err
	}
	userAccountID := int(userAccountID64)
	if userAccountID <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "invalid id parameter")
	}
	return userAccountID, nil
}

func parseAuthorizeSocialPlatformPayload(c fiber.Ctx) (harukiAPIHelper.AuthorizeSocialPlatformPayload, error) {
	var payload harukiAPIHelper.AuthorizeSocialPlatformPayload
	if encoding := c.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return payload, fiber.NewError(fiber.StatusUnsupportedMediaType, "compressed authorization bodies are not supported")
	}
	if len(c.Request().Body()) > 8<<10 {
		return payload, fiber.NewError(fiber.StatusRequestEntityTooLarge, "authorization body is too large")
	}
	if err := c.Bind().Body(&payload); err != nil {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, err
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatform(payload.Platform)) {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, fiber.NewError(fiber.StatusBadRequest, "unsupported platform")
	}
	payload.UserID = strings.TrimSpace(payload.UserID)
	if payload.UserID == "" {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, fiber.NewError(fiber.StatusBadRequest, "userId is required")
	}
	if len(payload.UserID) > 128 || len(payload.Comment) > 1024 || !utf8.ValidString(payload.UserID) || !utf8.ValidString(payload.Comment) {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, fiber.NewError(fiber.StatusBadRequest, "invalid authorization field length or encoding")
	}
	return payload, nil
}
