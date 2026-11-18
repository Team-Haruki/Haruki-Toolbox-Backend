package sponsor

import (
	"bytes"
	"crypto/subtle"
	json "encoding/json/v2"
	"errors"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

const (
	afdianCallbackRateLimitWindow = time.Minute
	afdianCallbackRateLimitMax    = 60
)

func RegisterSponsorRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, afdianConfig AfdianConfig) {
	apiHelper.Router.Get("/api/misc/sponsors", handleGetSponsors(apiHelper))
	apiHelper.Router.Get("/api/sponsor/afdian", handleGetSponsors(apiHelper))
	apiHelper.Router.Post("/api/sponsor/afdian/callback", handleAfdianCallback(apiHelper, afdianConfig))
	apiHelper.Router.Post("/api/sponsor/afdian/callback/:secret", handleAfdianCallback(apiHelper, afdianConfig))
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
	}
}

func handleAfdianCallback(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, cfg AfdianConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		secret := cfg.WebhookSecret()
		apiConfigured := cfg.CredentialsConfigured()

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

		if encoding := c.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
			return c.SendStatus(fiber.StatusUnsupportedMediaType)
		}
		body := c.Request().Body()
		if len(body) > 64<<10 {
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		// Charge traffic after the URL secret gate when that gate is enabled.
		// A throttled delivery must remain retryable by the payment provider.
		if apiHelper.DBManager != nil && apiHelper.DBManager.Redis != nil {
			if count, err := apiHelper.DBManager.Redis.IncrementWithTTL(c.Context(), harukiRedis.BuildAfdianCallbackRateLimitIPKey(c.IP()), afdianCallbackRateLimitWindow); err == nil && count > afdianCallbackRateLimitMax {
				c.Set("Retry-After", "60")
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"ec": 429})
			}
		}
		var payload map[string]any

		if err := json.UnmarshalRead(bytes.NewReader(body), &payload, jsonvalue.Numbers); err != nil {
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
			harukiLogger.Warnf("Afdian webhook order accepted via URL secret without API verification")
		case err != nil:
			harukiLogger.Warnf("Afdian webhook order verification request failed (%T)", err)
			return afdianAck(c)
		case !found:
			harukiLogger.Warnf("Afdian webhook order not found during API verification")
			return afdianAck(c)
		default:
			parsed = verified
		}

		if _, err := RecordAfdianOrder(c.Context(), apiHelper.DBManager.DB, parsed, now); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"ec": 500,
				"em": "failed to save sponsor order",
			})
		}

		return afdianAck(c)
	}
}
