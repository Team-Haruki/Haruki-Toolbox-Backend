package usersponsor

import (
	"regexp"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/gofiber/fiber/v3"
)

// A single full order number, never the OpenAPI's comma-separated batch form.
// Order numbers identify payments. Ownership comes from the payer OAuth link.
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
	uid, _ := usercore.CurrentUserID(c)
	payer, err := h.api.DBManager.DB.User.Get(c.Context(), uid)
	if err != nil {
		return api.ErrorInternal(c, "failed to load afdian binding")
	}
	if !hasAfdianOAuthProof(payer) {
		return api.ErrorBadRequest(c, "verify your afdian account with OAuth before verifying an order")
	}
	if err := h.orderRateLimit(c); err != nil {
		return err
	}
	now := time.Now().UTC()
	order, found, err := sponsor.VerifyAfdianOrder(c.Context(), h.orders, orderNumber, now)
	if err != nil {
		return fiber.NewError(502, "afdian order verification temporarily unavailable")
	}
	if !found || order.AfdianUserID != *payer.AfdianUserID {
		return api.ErrorBadRequest(c, "afdian order could not be verified for this account")
	}
	// Refresh a payment only for the proven payer. This never creates or
	// upgrades an identity claim.
	if _, err := sponsor.RecordAfdianOrder(c.Context(), h.api.DBManager.DB, order, now); err != nil {
		return api.ErrorInternal(c, "failed to record verified sponsorship")
	}
	usercore.WriteUserAuditLog(c, h.api, "user.afdian.verify_order", api.SystemLogResultSuccess, uid, nil)
	return h.status(c)
}
