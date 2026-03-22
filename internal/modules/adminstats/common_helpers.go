package adminstats

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"time"

	"github.com/gofiber/fiber/v3"
)

func respondFiberOrBadRequest(c fiber.Ctx, err error, fallbackMessage string) error {
	if fiberErr, ok := err.(*fiber.Error); ok {
		return c.Status(fiberErr.Code).JSON(fiber.Map{
			"status":  fiberErr.Code,
			"message": fiberErr.Message,
		})
	}
	return harukiAPIHelper.ErrorBadRequest(c, fallbackMessage)
}

var adminNow = time.Now

func adminNowUTC() time.Time {
	return adminNow().UTC()
}
