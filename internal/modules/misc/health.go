package misc

import (
	harukiHandler "haruki-suite/utils/handler"
	"time"

	"github.com/gofiber/fiber/v3"
)

func handleHealth() fiber.Handler {
	return func(c fiber.Ctx) error {
		loadedRegions, failedRegions := harukiHandler.GetSuiteRestorerLoadStatus()
		status := "ok"
		if len(failedRegions) > 0 {
			status = "degraded"
		}
		return c.JSON(fiber.Map{
			"status": status,
			"time":   time.Now().Unix(),
			"suiteRestorer": fiber.Map{
				"loadedRegions": loadedRegions,
				"failedRegions": failedRegions,
			},
		})
	}
}
