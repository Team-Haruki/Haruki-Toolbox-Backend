package sponsor

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func RegisterSponsorRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Get("/api/misc/sponsors", handleGetSponsors(apiHelper))
	apiHelper.Router.Get("/api/sponsor/afdian", handleGetSponsors(apiHelper))
	apiHelper.Router.Post("/api/sponsor/afdian/callback", handleAfdianCallback(apiHelper))
	apiHelper.Router.Post("/api/sponsor/afdian/callback/:secret", handleAfdianCallback(apiHelper))
}

// afdianAck returns the minimal response Afdian expects so it does not retry the
// webhook. Afdian only checks that "ec" equals 200.
func afdianAck(c fiber.Ctx) error {
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"ec": 200})
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
		cfg := config.Cfg.Afdian

		// First gate: the URL secret. Afdian webhooks are unsigned, so when a
		// webhook_secret is configured the caller must hit the secret path.
		if secret := strings.TrimSpace(cfg.WebhookSecret); secret != "" && c.Params("secret") != secret {
			harukiLogger.Warnf("Afdian webhook rejected: callback secret mismatch")
			return afdianAck(c)
		}

		var payload map[string]any
		decoder := json.NewDecoder(bytes.NewReader(c.Body()))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			return afdianAck(c)
		}

		now := time.Now().UTC()
		parsed, ok := ParseAfdianWebhookPayload(payload, now)
		if !ok {
			return afdianAck(c)
		}

		// Second gate: re-query the order via the Afdian API and trust that data
		// instead of the unsigned webhook body. A forged out_trade_no will not exist.
		verified, found, err := VerifyAfdianOrder(c.Context(), cfg, parsed.OutTradeNo, now)
		switch {
		case errors.Is(err, ErrAfdianNotConfigured):
			harukiLogger.Warnf("Afdian webhook order %q accepted without API verification: api credentials not configured", parsed.OutTradeNo)
		case err != nil:
			harukiLogger.Warnf("Afdian webhook order %q verification request failed: %v", parsed.OutTradeNo, err)
			return afdianAck(c)
		case !found:
			harukiLogger.Warnf("Afdian webhook order %q not found via API, rejecting possible forgery", parsed.OutTradeNo)
			return afdianAck(c)
		default:
			parsed = verified
		}

		if _, err := UpsertParsedSponsor(c.Context(), apiHelper.DBManager.DB, parsed, true); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"ec": 500,
				"em": "failed to save sponsor order",
			})
		}

		return afdianAck(c)
	}
}
