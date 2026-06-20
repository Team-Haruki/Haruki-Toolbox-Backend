package sponsor

import "time"

type SponsorItem struct {
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Avatar             string       `json:"avatar,omitempty"`
	Plan               *SponsorPlan `json:"plan,omitempty"`
	PlanID             string       `json:"planId,omitempty"`
	PlanName           string       `json:"planName"`
	PlanPrice          *float64     `json:"planPrice,omitempty"`
	PlanRank           int          `json:"planRank,omitempty"`
	PlanPayMonths      *int         `json:"planPayMonths,omitempty"`
	Message            string       `json:"message,omitempty"`
	Source             string       `json:"source"`
	IsActive           bool         `json:"isActive"`
	AfdianSyncDisabled bool         `json:"afdianSyncDisabled,omitempty"`
	TotalAmount        *float64     `json:"totalAmount,omitempty"`
	Month              *int         `json:"month,omitempty"`
	PaidAt             *time.Time   `json:"paidAt,omitempty"`
	PlanExpiresAt      *time.Time   `json:"planExpiresAt,omitempty"`
	SupportCount       int          `json:"supportCount"`
}

type SponsorPlan struct {
	ID        string     `json:"id,omitempty"`
	Name      string     `json:"name"`
	Title     string     `json:"title"`
	Rank      int        `json:"rank,omitempty"`
	PayMonth  *int       `json:"payMonth,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type SponsorSummary struct {
	SupporterCount int       `json:"supporterCount"`
	ActiveCount    int       `json:"activeCount"`
	OneTimeCount   int       `json:"oneTimeCount"`
	PastCount      int       `json:"pastCount"`
	GeneratedAt    time.Time `json:"generatedAt"`
}

type SponsorPageResponse struct {
	Summary    SponsorSummary `json:"summary"`
	Supporters []SponsorItem  `json:"supporters"`
}
