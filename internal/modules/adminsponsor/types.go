package adminsponsor

import "time"

type adminSponsorItem struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Avatar             string     `json:"avatar"`
	PlanName           string     `json:"planName"`
	Message            string     `json:"message"`
	Source             string     `json:"source"`
	Category           string     `json:"category"`
	IsActive           bool       `json:"isActive"`
	AfdianSyncDisabled bool       `json:"afdianSyncDisabled"`
	TotalAmount        *float64   `json:"totalAmount,omitzero"`
	Month              *int       `json:"month,omitzero"`
	PaidAt             *time.Time `json:"paidAt,omitzero"`
	// PlanExpiresAt is the effective expiry: Afdian time, then manual time.
	PlanExpiresAt *time.Time `json:"planExpiresAt,omitzero"`
	// AfdianExpiresAt and AfdianMonths are the Afdian part alone.
	AfdianExpiresAt *time.Time `json:"afdianExpiresAt,omitzero"`
	AfdianMonths    int        `json:"afdianMonths"`
	// DurationMigrationPending: the row still carries the legacy single
	// expiry; manual entries are accepted once the split has run.
	DurationMigrationPending bool      `json:"durationMigrationPending"`
	CreatedAt                time.Time `json:"createdAt"`
	UpdatedAt                time.Time `json:"updatedAt"`
}

type adminSponsorListResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Total       int                `json:"total"`
	Items       []adminSponsorItem `json:"items"`
}

// adminSponsorUpdatePayload edits the display profile. isActive and
// planExpiresAt are accepted for older clients and ignored: status and expiry
// are derived from Afdian orders and manual duration entries.
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

type adminSponsorCreatePayload struct {
	Name     string `json:"name"`
	Avatar   string `json:"avatar"`
	PlanName string `json:"planName"`
	Message  string `json:"message"`
}

type adminSponsorMutationResponse struct {
	Sponsor adminSponsorItem `json:"sponsor"`
}

type adminAfdianOrderItem struct {
	OutTradeNo  string    `json:"outTradeNo"`
	PlanID      string    `json:"planId"`
	PlanTitle   string    `json:"planTitle"`
	ProductType int       `json:"productType"`
	Month       int       `json:"month"`
	Kind        string    `json:"kind"`
	TotalAmount *float64  `json:"totalAmount,omitzero"`
	ShowAmount  *float64  `json:"showAmount,omitzero"`
	Remark      string    `json:"remark"`
	PaidAt      time.Time `json:"paidAt"`
}

type adminManualDurationItem struct {
	ID        int        `json:"id"`
	Amount    int        `json:"amount"`
	Unit      string     `json:"unit"`
	StartsAt  time.Time  `json:"startsAt"`
	Note      string     `json:"note"`
	Origin    string     `json:"origin"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitzero"`
}

// adminSponsorDetailResponse is one sponsor with both duration sources.
type adminSponsorDetailResponse struct {
	Sponsor adminSponsorItem `json:"sponsor"`
	Afdian  struct {
		ExpiresAt         *time.Time             `json:"expiresAt,omitzero"`
		Months            int                    `json:"months"`
		ReportedExpiresAt *time.Time             `json:"reportedExpiresAt,omitzero"`
		ReportedAt        *time.Time             `json:"reportedAt,omitzero"`
		Orders            []adminAfdianOrderItem `json:"orders"`
	} `json:"afdian"`
	ManualDurations    []adminManualDurationItem `json:"manualDurations"`
	EffectiveExpiresAt *time.Time                `json:"effectiveExpiresAt,omitzero"`
}

type adminManualDurationPayload struct {
	Amount   *int    `json:"amount,omitzero"`
	Unit     *string `json:"unit,omitzero"`
	StartsAt *string `json:"startsAt,omitzero"`
	Note     *string `json:"note,omitzero"`
}
