package sponsor

import "time"

type SponsorItem struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Avatar        string       `json:"avatar,omitempty"`
	Plan          *SponsorPlan `json:"plan,omitzero"`
	PlanID        string       `json:"planId,omitempty"`
	PlanName      string       `json:"planName"`
	PlanPrice     *float64     `json:"-"`
	PlanRank      int          `json:"-"`
	PlanPayMonths *int         `json:"planPayMonths,omitzero"`
	Message       string       `json:"message,omitempty"`
	Source        string       `json:"source"`
	// Category is decided by SponsorCategory; isActive mirrors category ==
	// "current" for older clients.
	Category           Category   `json:"category"`
	IsActive           bool       `json:"isActive"`
	AfdianSyncDisabled bool       `json:"afdianSyncDisabled,omitzero"`
	TotalAmount        *float64   `json:"-"`
	Month              *int       `json:"month,omitzero"`
	PaidAt             *time.Time `json:"paidAt,omitzero"`
	PlanExpiresAt      *time.Time `json:"planExpiresAt,omitzero"`
	SupportCount       int        `json:"supportCount"`
}

type SponsorPlan struct {
	ID        string     `json:"id,omitempty"`
	Name      string     `json:"name"`
	Title     string     `json:"title"`
	Rank      int        `json:"-"`
	PayMonth  *int       `json:"payMonth,omitzero"`
	ExpiresAt *time.Time `json:"expiresAt,omitzero"`
}

// SponsorSummary counts the two categories: activeCount is "current" and
// pastCount "former".
type SponsorSummary struct {
	SupporterCount int       `json:"supporterCount"`
	ActiveCount    int       `json:"activeCount"`
	PastCount      int       `json:"pastCount"`
	GeneratedAt    time.Time `json:"generatedAt"`
}

type SponsorPageResponse struct {
	Summary    SponsorSummary `json:"summary"`
	Supporters []SponsorItem  `json:"supporters"`
}
