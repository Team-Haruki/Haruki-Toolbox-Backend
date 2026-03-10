package adminusers

import (
	"haruki-suite/utils/database/postgresql"
	"testing"
	"time"
)

func TestApplyLatestExpiresAtByClient(t *testing.T) {
	t.Parallel()

	statsByClientID := map[int]adminOAuthTokenStats{
		1: {},
		2: {},
	}
	firstExpiry := time.Date(2026, time.March, 10, 10, 0, 0, 0, time.UTC)
	secondExpiry := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	shouldBeIgnored := time.Date(2026, time.March, 10, 8, 0, 0, 0, time.UTC)

	rows := []*postgresql.OAuthToken{
		{
			Edges: postgresql.OAuthTokenEdges{
				Client: &postgresql.OAuthClient{ID: 1},
			},
			ExpiresAt: &firstExpiry,
		},
		{
			Edges: postgresql.OAuthTokenEdges{
				Client: &postgresql.OAuthClient{ID: 1},
			},
			ExpiresAt: &secondExpiry,
		},
		{
			Edges: postgresql.OAuthTokenEdges{
				Client: &postgresql.OAuthClient{ID: 2},
			},
			ExpiresAt: nil,
		},
		{
			Edges: postgresql.OAuthTokenEdges{
				Client: &postgresql.OAuthClient{ID: 2},
			},
			ExpiresAt: &shouldBeIgnored,
		},
	}

	applyLatestExpiresAtByClient(statsByClientID, rows)

	if statsByClientID[1].LatestExpiresAt == nil || !statsByClientID[1].LatestExpiresAt.Equal(firstExpiry) {
		t.Fatalf("client 1 latestExpiresAt = %v, want %v", statsByClientID[1].LatestExpiresAt, firstExpiry)
	}
	if statsByClientID[2].LatestExpiresAt != nil {
		t.Fatalf("client 2 latestExpiresAt = %v, want nil", statsByClientID[2].LatestExpiresAt)
	}
}

func TestFinalizeAdminOAuthTokenStatsByClient(t *testing.T) {
	t.Parallel()

	statsByClientID := map[int]adminOAuthTokenStats{
		1: {Total: 2, Active: 5},
		2: {Total: 4, Active: 1},
	}

	finalizeAdminOAuthTokenStatsByClient(statsByClientID)

	if statsByClientID[1].Active != 2 || statsByClientID[1].Revoked != 0 {
		t.Fatalf("client 1 stats = %+v, want active=2 revoked=0", statsByClientID[1])
	}
	if statsByClientID[2].Active != 1 || statsByClientID[2].Revoked != 3 {
		t.Fatalf("client 2 stats = %+v, want active=1 revoked=3", statsByClientID[2])
	}
}
