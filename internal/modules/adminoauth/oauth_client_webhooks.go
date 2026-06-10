package adminoauth

import "time"

type adminOAuthClientWebhookPayload struct {
	CallbackURL string  `json:"callbackUrl"`
	Bearer      *string `json:"bearer,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	ClearBearer bool    `json:"clearBearer,omitempty"`
}

type adminOAuthClientWebhookItem struct {
	ID          string    `json:"id"`
	ClientID    string    `json:"clientId"`
	CallbackURL string    `json:"callbackUrl"`
	BearerSet   bool      `json:"bearerSet"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type adminOAuthClientWebhookListResponse struct {
	GeneratedAt time.Time                     `json:"generatedAt"`
	ClientID    string                        `json:"clientId"`
	Total       int                           `json:"total"`
	Items       []adminOAuthClientWebhookItem `json:"items"`
}

type adminOAuthClientWebhookMutationResponse struct {
	GeneratedAt time.Time                   `json:"generatedAt"`
	ClientID    string                      `json:"clientId"`
	Webhook     adminOAuthClientWebhookItem `json:"webhook"`
}
