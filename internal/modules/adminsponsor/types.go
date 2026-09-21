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
	TotalAmount        *float64   `json:"totalAmount,omitzero"`
	Month              *int       `json:"month,omitzero"`
	PaidAt             *time.Time `json:"paidAt,omitzero"`
	PlanExpiresAt      *time.Time `json:"planExpiresAt,omitzero"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type adminSponsorListResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Total       int                `json:"total"`
	Items       []adminSponsorItem `json:"items"`
}

type adminSponsorUpdatePayload struct {
	Name               *string `json:"name,omitzero"`
	Avatar             *string `json:"avatar,omitzero"`
	PlanName           *string `json:"planName,omitzero"`
	Message            *string `json:"message,omitzero"`
	Source             *string `json:"source,omitzero"`
	IsActive           *bool   `json:"isActive,omitzero"`
	AfdianSyncDisabled *bool   `json:"afdianSyncDisabled,omitzero"`
	PaidAt             *string `json:"paidAt,omitzero"`
	PlanExpiresAt      *string `json:"planExpiresAt,omitzero"`
}

type adminSponsorMutationResponse struct {
	Sponsor adminSponsorItem `json:"sponsor"`
}
