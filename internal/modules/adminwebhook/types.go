package adminwebhook

import "time"

type adminWebhookSettingsPayload struct {
	Enabled   *bool   `json:"enabled,omitzero"`
	JWTSecret *string `json:"jwtSecret,omitzero"`
}

type adminWebhookSettingsResponse struct {
	Enabled             bool `json:"enabled"`
	JWTSecretConfigured bool `json:"jwtSecretConfigured"`
}

type adminWebhookPayload struct {
	ID          *string `json:"id,omitzero"`
	Credential  *string `json:"credential,omitzero"`
	CallbackURL *string `json:"callbackUrl,omitzero"`
	Bearer      *string `json:"bearer,omitzero"`
	Enabled     *bool   `json:"enabled,omitzero"`
	ClearBearer bool    `json:"clearBearer,omitzero"`
}

type adminWebhookItem struct {
	ID                string     `json:"id"`
	Credential        string     `json:"credential"`
	CallbackURL       string     `json:"callbackUrl"`
	Bearer            *string    `json:"bearer,omitzero"`
	Enabled           bool       `json:"enabled"`
	SubscriptionCount int        `json:"subscriptionCount"`
	CreatedAt         *time.Time `json:"createdAt,omitzero"`
}

type adminWebhookListResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Total       int                `json:"total"`
	Items       []adminWebhookItem `json:"items"`
}

type adminWebhookSubscriberItem struct {
	UserID    string     `json:"userId"`
	Server    string     `json:"server"`
	DataType  string     `json:"dataType"`
	CreatedAt *time.Time `json:"createdAt,omitzero"`
}

type adminWebhookMutationResponse struct {
	Webhook         adminWebhookItem `json:"webhook"`
	Token           string           `json:"token"`
	TokenHeaderName string           `json:"tokenHeaderName"`
}

type adminWebhookSubscribersResponse struct {
	GeneratedAt time.Time                    `json:"generatedAt"`
	WebhookID   string                       `json:"webhookId"`
	Total       int                          `json:"total"`
	Items       []adminWebhookSubscriberItem `json:"items"`
}
