package admin

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"

	"github.com/gofiber/fiber/v3"
)

func respondFiberOrBadRequest(c fiber.Ctx, err error, fallbackMessage string) error {
	return adminCoreModule.RespondFiberOrBadRequest(c, err, fallbackMessage)
}

func respondFiberOrUnauthorized(c fiber.Ctx, err error, fallbackMessage string) error {
	return adminCoreModule.RespondFiberOrUnauthorized(c, err, fallbackMessage)
}

func respondFiberOrInternal(c fiber.Ctx, err error, fallbackMessage string) error {
	return adminCoreModule.RespondFiberOrInternal(c, err, fallbackMessage)
}

func respondFiberOrForbidden(c fiber.Ctx, err error, fallbackMessage string) error {
	return adminCoreModule.RespondFiberOrForbidden(c, err, fallbackMessage)
}
