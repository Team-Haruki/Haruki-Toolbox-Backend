//go:build legacy_oauth

package adminusers

import "haruki-suite/utils/database/postgresql"

func applyLatestExpiresAtByClient(statsByClientID map[int]adminOAuthTokenStats, rows []*postgresql.OAuthToken) {
	seenLatestByClientID := make(map[int]struct{}, len(statsByClientID))
	for _, row := range rows {
		if row == nil || row.Edges.Client == nil {
			continue
		}
		clientID := row.Edges.Client.ID
		if _, seen := seenLatestByClientID[clientID]; seen {
			continue
		}
		seenLatestByClientID[clientID] = struct{}{}
		if row.ExpiresAt == nil {
			continue
		}
		stats := statsByClientID[clientID]
		expiresAt := row.ExpiresAt.UTC()
		stats.LatestExpiresAt = &expiresAt
		statsByClientID[clientID] = stats
	}
}

func finalizeAdminOAuthTokenStatsByClient(statsByClientID map[int]adminOAuthTokenStats) {
	for clientID, stats := range statsByClientID {
		if stats.Active > stats.Total {
			stats.Active = stats.Total
		}
		stats.Revoked = stats.Total - stats.Active
		statsByClientID[clientID] = stats
	}
}
