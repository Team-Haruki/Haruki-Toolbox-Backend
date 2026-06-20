package adminsponsor

import "time"

type adminSponsorItem struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Avatar             string     `json:"avatar"`
	PlanName           string     `json:"planName"`
	Message            string     `json:"message"`
	Source             string     `json:"source"`
	IsActive           bool       `json:"isActive"`
	AfdianSyncDisabled bool       `json:"afdianSyncDisabled"`
	TotalAmount        *float64   `json:"totalAmount,omitempty"`
	Month              *int       `json:"month,omitempty"`
	PaidAt             *time.Time `json:"paidAt,omitempty"`
	PlanExpiresAt      *time.Time `json:"planExpiresAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type adminSponsorListResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Total       int                `json:"total"`
	Items       []adminSponsorItem `json:"items"`
}

type adminSponsorUpdatePayload struct {
	Name               *string `json:"name,omitempty"`
	Avatar             *string `json:"avatar,omitempty"`
	PlanName           *string `json:"planName,omitempty"`
	Message            *string `json:"message,omitempty"`
	Source             *string `json:"source,omitempty"`
	IsActive           *bool   `json:"isActive,omitempty"`
	AfdianSyncDisabled *bool   `json:"afdianSyncDisabled,omitempty"`
	PaidAt             *string `json:"paidAt,omitempty"`
	PlanExpiresAt      *string `json:"planExpiresAt,omitempty"`
}

type adminSponsorMutationResponse struct {
	Sponsor adminSponsorItem `json:"sponsor"`
}
