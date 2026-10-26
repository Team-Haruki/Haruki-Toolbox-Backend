package sponsor

import (
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

func TestToolboxAccessCombinesHistoricalProofAndAdminDecisions(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	id, other := "verified-payer", "other-payer"
	order := user.AfdianVerificationMethodOrder
	granted, blocked := user.SponsorAccessOverrideGranted, user.SponsorAccessOverrideBlocked
	rows := []*postgresql.Sponsor{{AfdianUserID: &other, PlanExpiresAt: &future}, {AfdianUserID: nil}, {AfdianUserID: &id, PlanExpiresAt: &past}}
	for _, tc := range []struct {
		name                       string
		owner                      postgresql.User
		eligible                   bool
		category, method, override string
	}{
		{"unlinked cannot borrow a sponsor", postgresql.User{}, false, "none", "", ""},
		{"legacy historical OAuth", postgresql.User{AfdianUserID: &id}, true, "former", "oauth2", ""},
		{"historical order", postgresql.User{AfdianUserID: &id, AfdianVerificationMethod: &order}, true, "former", "order", ""},
		{"manual without provider", postgresql.User{SponsorAccessOverride: &granted}, true, "former", "", "granted"},
		{"block overrides paid proof", postgresql.User{AfdianUserID: &id, SponsorAccessOverride: &blocked}, false, "former", "oauth2", "blocked"},
		{"ban overrides manual grant", postgresql.User{Banned: true, SponsorAccessOverride: &granted}, false, "former", "", "granted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ResolveToolboxAccess(&tc.owner, rows, now)
			if s.Eligible != tc.eligible || s.Category != tc.category || s.VerificationMethod != tc.method || s.AccessOverride != tc.override {
				t.Fatalf("unexpected access: %+v", s)
			}
			if tc.owner.AfdianUserID != nil && (s.ExpiresAt == nil || !s.ExpiresAt.Equal(past)) {
				t.Fatal("borrowed another payer's current expiry")
			}
		})
	}
	rows = append(rows, &postgresql.Sponsor{AfdianUserID: &id, PlanExpiresAt: &future})
	s := ResolveToolboxAccess(&postgresql.User{AfdianUserID: &id}, rows, now)
	if !s.Eligible || s.Category != "current" || s.ExpiresAt == nil || !s.ExpiresAt.Equal(future) {
		t.Fatalf("current proof did not take precedence: %+v", s)
	}
	s = ResolveToolboxAccess(&postgresql.User{AfdianUserID: &id}, nil, now)
	if s.Eligible {
		t.Fatal("linked but never-sponsored identity was eligible")
	}
}

func TestQueryToolboxAccessDoesNotGrantOnDatabaseFailure(t *testing.T) {
	db := openSponsorDB(t)
	ctx := t.Context()
	id := "verified-payer"
	u := &postgresql.User{AfdianUserID: &id}
	db.Sponsor.Create().SetID("sponsor").SetAfdianUserID(id).SaveX(ctx)
	s, err := QueryToolboxAccess(ctx, db, u)
	if err != nil || !s.Eligible {
		t.Fatalf("stored proof missing: %+v, %v", s, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = QueryToolboxAccess(ctx, db, u)
	if err == nil || s.Eligible {
		t.Fatal("database failure granted access")
	}
	granted := user.SponsorAccessOverrideGranted
	s, err = QueryToolboxAccess(ctx, db, &postgresql.User{SponsorAccessOverride: &granted})
	if err != nil || !s.Eligible {
		t.Fatal("unlinked manual access unnecessarily depends on sponsor rows")
	}
}
