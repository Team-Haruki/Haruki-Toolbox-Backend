package sponsor

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

const (
	afdianCallbackRateLimitWindow = time.Minute
	afdianCallbackRateLimitMax    = 60
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

		// Per-IP rate limit: the callback is public and triggers an outbound
		// Afdian API verification, so bound abuse/amplification per source.
		if apiHelper.DBManager != nil && apiHelper.DBManager.Redis != nil {
			if count, err := apiHelper.DBManager.Redis.IncrementWithTTL(c.Context(), harukiRedis.BuildAfdianCallbackRateLimitIPKey(c.IP()), afdianCallbackRateLimitWindow); err == nil && count > afdianCallbackRateLimitMax {
				harukiLogger.Warnf("Afdian webhook rate limited for IP %s", c.IP())
				return afdianAck(c)
			}
		}

		secret := strings.TrimSpace(cfg.WebhookSecret)
		apiConfigured := strings.TrimSpace(cfg.UserID) != "" && strings.TrimSpace(cfg.APIToken) != ""

		// Fail closed: Afdian webhooks are unsigned, so with neither authenticity
		// gate configured (no URL secret AND no API credentials to re-verify) the
		// body cannot be trusted. Refuse to persist it rather than accept forgery.
		if secret == "" && !apiConfigured {
			harukiLogger.Warnf("Afdian webhook rejected: neither webhook secret nor API credentials are configured")
			return afdianAck(c)
		}

		// First gate: the URL secret, compared in constant time. When configured,
		// the caller must hit the secret path.
		if secret != "" {
			provided := strings.TrimSpace(c.Params("secret"))
			if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
				harukiLogger.Warnf("Afdian webhook rejected: callback secret mismatch")
				return afdianAck(c)
			}
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
			// Reachable only when the URL secret gate above passed, so the body is
			// trusted by virtue of the secret even though API re-verification is off.
			harukiLogger.Warnf("Afdian webhook order %q accepted via URL secret without API verification", parsed.OutTradeNo)
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
