package sponsor

import (
	"context"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

// ToolboxAccess is shared by the user settings, admin manager and bot frame API.
// Administrative notes and actor IDs never appear in this public-to-owner view.
type ToolboxAccess struct {
	VerificationMethod string     `json:"verificationMethod"`
	AfdianUserID       *string    `json:"afdianUserId"`
	BoundAt            *time.Time `json:"boundAt"`
	Eligible           bool       `json:"eligible"`
	Category           string     `json:"category"`
	ExpiresAt          *time.Time `json:"expiresAt"`
	AccessOverride     string     `json:"accessOverride"`
}

func ResolveToolboxAccess(u *postgresql.User, rows []*postgresql.Sponsor, now time.Time) ToolboxAccess {
	s := ToolboxAccess{AfdianUserID: u.AfdianUserID, BoundAt: u.AfdianBoundAt, Category: "none"}
	if u.AfdianUserID != nil {
		s.VerificationMethod = "oauth2" // Links created before order verification.
		if u.AfdianVerificationMethod != nil {
			s.VerificationMethod = string(*u.AfdianVerificationMethod)
		}
		for _, row := range rows {
			if row.AfdianUserID == nil || *row.AfdianUserID != *u.AfdianUserID {
				continue
			}
			s.Eligible = true // Former sponsorship also grants permanent access.
			category := string(SponsorCategory(row, now))
			if s.Category == "none" || category == string(CategoryCurrent) {
				s.Category = category
			}
			if row.PlanExpiresAt != nil && (s.ExpiresAt == nil || row.PlanExpiresAt.After(*s.ExpiresAt)) {
				s.ExpiresAt = row.PlanExpiresAt
			}
		}
	}
	if u.SponsorAccessOverride != nil {
		s.AccessOverride = string(*u.SponsorAccessOverride)
		switch *u.SponsorAccessOverride {
		case user.SponsorAccessOverrideGranted:
			s.Eligible = true
			if s.Category == "none" {
				s.Category = string(CategoryFormer)
			}
		case user.SponsorAccessOverrideBlocked:
			s.Eligible = false
		}
	}
	if u.Banned {
		s.Eligible = false
	}
	return s
}

func QueryToolboxAccess(ctx context.Context, db *postgresql.Client, u *postgresql.User) (ToolboxAccess, error) {
	var rows []*postgresql.Sponsor
	if u.AfdianUserID != nil {
		var err error
		rows, err = db.Sponsor.Query().Where(sponsorSchema.AfdianUserIDEQ(*u.AfdianUserID)).All(ctx)
		if err != nil {
			return ToolboxAccess{}, err
		}
	}
	return ResolveToolboxAccess(u, rows, time.Now().UTC()), nil
}
