package usersponsor

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/gofiber/fiber/v3"
)

// A single full order number, never the OpenAPI's comma-separated batch form.
// Order numbers are proof credentials: keep them out of URLs, keys and logs.
var orderNumberPattern = regexp.MustCompile(`^[0-9]{16,128}$`)

func (h *handlers) orderRateLimit(c fiber.Ctx) error {
	manager := h.api.DBManager.Redis
	if manager == nil || manager.Redis == nil {
		return fiber.NewError(503, "order verification temporarily unavailable")
	}
	uid, _ := usercore.CurrentUserID(c)
	for _, limit := range []struct {
		scope, userID string
		ttl           time.Duration
		max           int64
	}{
		{"user-short", uid, 10 * time.Minute, 5},
		{"user-day", uid, 24 * time.Hour, 20},
		{"global", "all", time.Minute, 120},
	} {
		count, err := manager.IncrementWithTTL(c.Context(), manager.KeyBuilder().BuildAfdianOrderRateKey(limit.scope, limit.userID), limit.ttl)
		if err != nil {
			return fiber.NewError(503, "order verification temporarily unavailable")
		}
		if count > limit.max {
			return fiber.NewError(429, "too many order verification attempts")
		}
	}
	return nil
}

func (h *handlers) verifyOrder(c fiber.Ctx) error {
	if !h.orders.CredentialsConfigured() {
		return fiber.NewError(503, "afdian order verification is not configured")
	}
	if err := h.orderRateLimit(c); err != nil {
		return err
	}
	var payload struct {
		OrderNumber string `json:"orderNumber"`
	}
	if c.Bind().Body(&payload) != nil {
		return api.ErrorBadRequest(c, "invalid afdian order number")
	}
	orderNumber := strings.TrimSpace(payload.OrderNumber)
	if !orderNumberPattern.MatchString(orderNumber) {
		return api.ErrorBadRequest(c, "invalid afdian order number")
	}
	now := time.Now().UTC()
	order, found, err := sponsor.VerifyAfdianOrder(c.Context(), h.orders, orderNumber, now)
	if err != nil {
		return fiber.NewError(502, "afdian order verification temporarily unavailable")
	}
	// Unpaid, missing, foreign-creator and already-claimed orders all have the
	// same public error. Only the authoritative response supplies the user ID.
	const rejected = "afdian order could not be verified or is already linked"
	if !found {
		return api.ErrorBadRequest(c, rejected)
	}
	uid, _ := usercore.CurrentUserID(c)
	// Record the verified payment using the existing idempotent order pipeline;
	// this also handles a new payment not yet picked up by scheduled sync.
	if _, err := sponsor.RecordAfdianOrder(c.Context(), h.api.DBManager.DB, order, now); err != nil {
		return api.ErrorInternal(c, "failed to record verified sponsorship")
	}
	if err := h.bindIdentity(c.Context(), uid, order.AfdianUserID, "order"); err != nil {
		if errors.Is(err, errBindingConflict) {
			return api.ErrorBadRequest(c, rejected)
		}
		return api.ErrorInternal(c, "failed to save afdian binding")
	}
	usercore.WriteUserAuditLog(c, h.api, "user.afdian.bind", api.SystemLogResultSuccess, uid, map[string]any{"verificationMethod": "order"})
	return h.status(c)
}
