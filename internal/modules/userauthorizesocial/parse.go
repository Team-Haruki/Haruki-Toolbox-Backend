package userauthorizesocial

import (
	"strconv"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

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
	if err := c.Bind().Body(&payload); err != nil {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, err
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatform(payload.Platform)) {
		return harukiAPIHelper.AuthorizeSocialPlatformPayload{}, fiber.NewError(fiber.StatusBadRequest, "unsupported platform")
	}
	return payload, nil
}
