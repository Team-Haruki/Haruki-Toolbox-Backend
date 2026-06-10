package adminoauth

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"

	"github.com/gofiber/fiber/v3"
)

func generateAdminOAuthClientWebhookID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func sanitizeAdminOAuthWebhookCallbackURL(raw string) (string, error) {
	callbackURL, ok := harukiHandler.ValidateWebhookCallbackURL(raw)
	if !ok {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid callbackUrl")
	}
	return callbackURL, nil
}

func sanitizeAdminOAuthWebhookBearer(raw *string) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func buildAdminOAuthClientWebhookItem(row *postgresql.OAuth2ClientWebhookEndpoint) adminOAuthClientWebhookItem {
	item := adminOAuthClientWebhookItem{}
	if row == nil {
		return item
	}
	item.ID = row.ID
	item.ClientID = row.ClientID
	item.CallbackURL = row.CallbackURL
	item.Enabled = row.Enabled
	item.CreatedAt = row.CreatedAt.UTC()
	item.UpdatedAt = row.UpdatedAt.UTC()
	item.BearerSet = row.Bearer != nil && strings.TrimSpace(*row.Bearer) != ""
	return item
}

func buildAdminOAuthClientWebhookMutationResponse(row *postgresql.OAuth2ClientWebhookEndpoint) adminOAuthClientWebhookMutationResponse {
	item := buildAdminOAuthClientWebhookItem(row)
	return adminOAuthClientWebhookMutationResponse{
		GeneratedAt: adminNowUTC(),
		ClientID:    item.ClientID,
		Webhook:     item,
	}
}
