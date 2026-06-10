package adminwebhook

import "time"

type adminWebhookSettingsPayload struct {
	Enabled   *bool   `json:"enabled,omitempty"`
	JWTSecret *string `json:"jwtSecret,omitempty"`
}

type adminWebhookSettingsResponse struct {
	Enabled             bool `json:"enabled"`
	JWTSecretConfigured bool `json:"jwtSecretConfigured"`
}

type adminWebhookPayload struct {
	ID          *string `json:"id,omitempty"`
	Credential  *string `json:"credential,omitempty"`
	CallbackURL *string `json:"callbackUrl,omitempty"`
	Bearer      *string `json:"bearer,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	ClearBearer bool    `json:"clearBearer,omitempty"`
}

type adminWebhookItem struct {
	ID                string     `json:"id"`
	Credential        string     `json:"credential"`
	CallbackURL       string     `json:"callbackUrl"`
	Bearer            *string    `json:"bearer,omitempty"`
	Enabled           bool       `json:"enabled"`
	SubscriptionCount int        `json:"subscriptionCount"`
	CreatedAt         *time.Time `json:"createdAt,omitempty"`
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
	CreatedAt *time.Time `json:"createdAt,omitempty"`
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
