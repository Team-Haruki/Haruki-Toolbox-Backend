package adminwebhook

import (
	webhookModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/webhook"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"strings"
)

func buildAdminWebhookSettingsResponse(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) adminWebhookSettingsResponse {
	return adminWebhookSettingsResponse{
		Enabled:             apiHelper.GetWebhookEnabled(),
		JWTSecretConfigured: strings.TrimSpace(apiHelper.GetWebhookJWTSecret()) != "",
	}
}

func buildAdminWebhookItem(row *postgresql.WebhookEndpoint) adminWebhookItem {
	item := adminWebhookItem{
		ID:                row.ID,
		Credential:        row.Credential,
		CallbackURL:       row.CallbackURL,
		Enabled:           row.Enabled,
		SubscriptionCount: len(row.Edges.Subscriptions),
	}
	if row.Bearer != nil && strings.TrimSpace(*row.Bearer) != "" {
		bearer := strings.TrimSpace(*row.Bearer)
		item.Bearer = &bearer
	}
	createdAt := row.CreatedAt.UTC()
	item.CreatedAt = &createdAt
	return item
}

func buildAdminWebhookMutationResponse(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, row *postgresql.WebhookEndpoint) adminWebhookMutationResponse {
	resp := adminWebhookMutationResponse{
		Webhook:         buildAdminWebhookItem(row),
		TokenHeaderName: webhookModule.TokenHeaderName,
	}
	secret := strings.TrimSpace(apiHelper.GetWebhookJWTSecret())
	if secret == "" {
		return resp
	}
	token, err := webhookModule.SignWebhookToken(secret, row.ID, row.Credential)
	if err == nil {
		resp.Token = token
	}
	return resp
}
