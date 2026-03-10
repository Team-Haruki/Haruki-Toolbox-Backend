package adminoauth

import (
	"haruki-suite/utils/database/postgresql"
	"testing"
	"time"
)

func TestApplyLatestExpiresAtByUser(t *testing.T) {
	t.Parallel()

	statsByUserID := map[string]adminOAuthTokenStats{
		"u1": {},
		"u2": {},
	}
	firstExpiry := time.Date(2026, time.March, 10, 10, 0, 0, 0, time.UTC)
	secondExpiry := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	shouldBeIgnored := time.Date(2026, time.March, 10, 8, 0, 0, 0, time.UTC)

	rows := []*postgresql.OAuthToken{
		{
			Edges: postgresql.OAuthTokenEdges{
				User: &postgresql.User{ID: "u1"},
			},
			ExpiresAt: &firstExpiry,
		},
		{
			Edges: postgresql.OAuthTokenEdges{
				User: &postgresql.User{ID: "u1"},
			},
			ExpiresAt: &secondExpiry,
		},
		{
			Edges: postgresql.OAuthTokenEdges{
				User: &postgresql.User{ID: "u2"},
			},
			ExpiresAt: nil,
		},
		{
			Edges: postgresql.OAuthTokenEdges{
				User: &postgresql.User{ID: "u2"},
			},
			ExpiresAt: &shouldBeIgnored,
		},
	}

	applyLatestExpiresAtByUser(statsByUserID, rows)

	if statsByUserID["u1"].LatestExpiresAt == nil || !statsByUserID["u1"].LatestExpiresAt.Equal(firstExpiry) {
		t.Fatalf("user u1 latestExpiresAt = %v, want %v", statsByUserID["u1"].LatestExpiresAt, firstExpiry)
	}
	if statsByUserID["u2"].LatestExpiresAt != nil {
		t.Fatalf("user u2 latestExpiresAt = %v, want nil", statsByUserID["u2"].LatestExpiresAt)
	}
}

func TestFinalizeAdminOAuthTokenStatsByUser(t *testing.T) {
	t.Parallel()

	statsByUserID := map[string]adminOAuthTokenStats{
		"u1": {Total: 2, Active: 5},
		"u2": {Total: 4, Active: 1},
	}

	finalizeAdminOAuthTokenStatsByUser(statsByUserID)

	if statsByUserID["u1"].Active != 2 || statsByUserID["u1"].Revoked != 0 {
		t.Fatalf("user u1 stats = %+v, want active=2 revoked=0", statsByUserID["u1"])
	}
	if statsByUserID["u2"].Active != 1 || statsByUserID["u2"].Revoked != 3 {
		t.Fatalf("user u2 stats = %+v, want active=1 revoked=3", statsByUserID["u2"])
	}
}
