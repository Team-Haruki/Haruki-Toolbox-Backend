package sponsor

import (
	"bytes"
	"crypto/subtle"
	"encoding/json/jsontext"
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
	AfdianCallbackPath            = "/api/sponsor/afdian/callback"
	AfdianCallbackBodyLimit       = 64 << 10
	afdianCallbackRateLimitWindow = time.Minute
	afdianCallbackRateLimitMax    = 60
	afdianCallbackConcurrency     = 8
)

var afdianCallbackSlots = make(chan struct{}, afdianCallbackConcurrency)

func RegisterSponsorRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, afdianConfig AfdianConfig) {
	apiHelper.Router.Get("/api/misc/sponsors", handleGetSponsors(apiHelper))
	apiHelper.Router.Get("/api/sponsor/afdian", handleGetSponsors(apiHelper))
	apiHelper.Router.Post(AfdianCallbackPath, handleAfdianCallback(apiHelper, afdianConfig))
	apiHelper.Router.Post(AfdianCallbackPath+"/:secret", handleAfdianCallback(apiHelper, afdianConfig))
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
		if len(body) > AfdianCallbackBodyLimit {
			return c.SendStatus(fiber.StatusRequestEntityTooLarge)
		}
		select {
		case afdianCallbackSlots <- struct{}{}:
			defer func() { <-afdianCallbackSlots }()
		default:
			c.Set("Retry-After", "2")
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"ec": 503})
		}
		// Charge traffic after the URL secret gate when that gate is enabled.
		// A throttled delivery must remain retryable by the payment provider.
		if apiHelper.DBManager != nil && apiHelper.DBManager.Redis != nil {
			if count, err := apiHelper.DBManager.Redis.IncrementWithTTL(c.Context(), harukiRedis.BuildAfdianCallbackRateLimitIPKey(c.IP()), afdianCallbackRateLimitWindow); err == nil && count > afdianCallbackRateLimitMax {
				c.Set("Retry-After", "60")
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"ec": 429})
			}
		}
		now := time.Now().UTC()
		parsed, ok := parseAfdianCallbackBody(body, now, apiConfigured)
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

// For API-backed delivery, only extract the order reference and the fields
// needed to check its shape. The authenticated provider supplies the details.
func parseAfdianCallbackBody(body []byte, now time.Time, apiConfigured bool) (parsedAfdianOrder, bool) {
	if !apiConfigured {
		var payload map[string]any
		if err := json.UnmarshalRead(bytes.NewReader(body), &payload, jsonvalue.Numbers); err != nil {
			return parsedAfdianOrder{}, false
		}
		return ParseAfdianWebhookPayload(payload, now)
	}
	var envelope struct {
		Order jsontext.Value `json:"order"`
		Data  jsontext.Value `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return parsedAfdianOrder{}, false
	}
	var data struct {
		Order jsontext.Value `json:"order"`
	}
	order := envelope.Order
	if envelope.Data.Kind() == '{' && json.Unmarshal(envelope.Data, &data) == nil && data.Order.Kind() == '{' {
		order = data.Order
	}
	var fields struct {
		Status       jsontext.Value `json:"status"`
		TradeNo      jsontext.Value `json:"out_trade_no"`
		TradeNoCamel jsontext.Value `json:"outTradeNo"`
		UserID       jsontext.Value `json:"user_id"`
		UserIDCamel  jsontext.Value `json:"userId"`
	}
	if order.Kind() != '{' || json.Unmarshal(order, &fields) != nil {
		return parsedAfdianOrder{}, false
	}
	selected := make(map[string]any, 5)
	for name, raw := range map[string]jsontext.Value{"status": fields.Status, "out_trade_no": fields.TradeNo, "outTradeNo": fields.TradeNoCamel, "user_id": fields.UserID, "userId": fields.UserIDCamel} {
		if kind := raw.Kind(); kind == '"' || kind == '0' {
			var value any
			if json.Unmarshal(raw, &value, jsonvalue.Numbers) == nil {
				selected[name] = value
			}
		}
	}
	return parseAfdianOrder(selected, now)
}
