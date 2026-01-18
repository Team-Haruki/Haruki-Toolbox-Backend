package misc

import (
	"time"

	"github.com/gofiber/fiber/v3"
)

func handleHealth() fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status": "ok",
			"time":   time.Now().Unix(),
		})
	}
}

func registerHealthRoutes(router fiber.Router) {
	router.Get("/health", handleHealth())
}
