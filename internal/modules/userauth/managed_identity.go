package userauth

import (
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

const ManagedIdentityMessage = "browser identity is managed by Ory Kratos; use Kratos self-service flows instead"

func LegacyAuthDisabledHandler() fiber.Handler {
	return func(c fiber.Ctx) error {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusGone, ManagedIdentityMessage, nil)
	}
}
