package sponsor

import (
	"bytes"
	"encoding/json"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RegisterSponsorRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Get("/api/misc/sponsors", handleGetSponsors(apiHelper))
	apiHelper.Router.Get("/api/sponsor/afdian", handleGetSponsors(apiHelper))
	apiHelper.Router.Post("/api/sponsor/afdian/callback", handleAfdianCallback(apiHelper))
}

func handleGetSponsors(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		rows, err := QuerySponsors(c.Context(), apiHelper.DBManager.DB)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query sponsors")
		}
		resp := BuildSponsorPageResponse(rows, time.Now().UTC())
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAfdianCallback(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload map[string]any
		decoder := json.NewDecoder(bytes.NewReader(c.Body()))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			return c.Status(fiber.StatusOK).JSON(fiber.Map{"ec": 200})
		}

		if parsed, ok := ParseAfdianWebhookPayload(payload, time.Now().UTC()); ok {
			if _, err := UpsertParsedSponsor(c.Context(), apiHelper.DBManager.DB, parsed, true); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"ec": 500,
					"em": "failed to save sponsor order",
				})
			}
		}

		return c.Status(fiber.StatusOK).JSON(fiber.Map{"ec": 200})
	}
}
