package adminusers

import (
	"context"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	"haruki-suite/utils/database/postgresql/predicate"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"time"

	sql "entgo.io/ent/dialect/sql"
)

func queryUserOAuthTokenStatsByClients(ctx context.Context, db *postgresql.Client, userID string, clientDBIDs []int) (map[int]adminOAuthTokenStats, error) {
	if len(clientDBIDs) == 0 {
		return map[int]adminOAuthTokenStats{}, nil
	}

	unique := make([]int, 0, len(clientDBIDs))
	seen := make(map[int]struct{}, len(clientDBIDs))
	for _, clientDBID := range clientDBIDs {
		if clientDBID <= 0 {
			continue
		}
		if _, ok := seen[clientDBID]; ok {
			continue
		}
		seen[clientDBID] = struct{}{}
		unique = append(unique, clientDBID)
	}
	if len(unique) == 0 {
		return map[int]adminOAuthTokenStats{}, nil
	}

	statsByClientID := make(map[int]adminOAuthTokenStats, len(unique))
	for _, clientDBID := range unique {
		statsByClientID[clientDBID] = adminOAuthTokenStats{}
	}

	baseQuery := db.OAuthToken.Query().Where(
		oauthtoken.HasUserWith(userSchema.IDEQ(userID)),
		oauthtoken.HasClientWith(oauthclient.IDIn(unique...)),
	)

	var totalRows []struct {
		ClientID int `json:"oauth_client_tokens"`
		Count    int `json:"count"`
	}
	if err := baseQuery.Clone().
		GroupBy(oauthtoken.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &totalRows); err != nil {
		return nil, err
	}
	for _, row := range totalRows {
		stats := statsByClientID[row.ClientID]
		stats.Total = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var activeRows []struct {
		ClientID int `json:"oauth_client_tokens"`
		Count    int `json:"count"`
	}
	if err := baseQuery.Clone().
		Where(oauthtoken.RevokedEQ(false)).
		GroupBy(oauthtoken.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &activeRows); err != nil {
		return nil, err
	}
	for _, row := range activeRows {
		stats := statsByClientID[row.ClientID]
		stats.Active = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var latestIssuedRows []struct {
		ClientID       int       `json:"oauth_client_tokens"`
		LatestIssuedAt time.Time `json:"latest_issued_at"`
	}
	if err := baseQuery.Clone().
		GroupBy(oauthtoken.ClientColumn).
		Aggregate(postgresql.As(postgresql.Max(oauthtoken.FieldCreatedAt), "latest_issued_at")).
		Scan(ctx, &latestIssuedRows); err != nil {
		return nil, err
	}
	latestPredicates := make([]predicate.OAuthToken, 0, len(latestIssuedRows))
	for _, row := range latestIssuedRows {
		stats := statsByClientID[row.ClientID]
		issuedAt := row.LatestIssuedAt.UTC()
		stats.LatestIssuedAt = &issuedAt
		statsByClientID[row.ClientID] = stats

		latestPredicates = append(latestPredicates, oauthtoken.And(
			oauthtoken.HasClientWith(oauthclient.IDEQ(row.ClientID)),
			oauthtoken.CreatedAtEQ(row.LatestIssuedAt),
		))
	}

	if len(latestPredicates) > 0 {
		latestTokens, err := db.OAuthToken.Query().Where(
			oauthtoken.HasUserWith(userSchema.IDEQ(userID)),
			oauthtoken.Or(latestPredicates...),
		).WithClient().Order(
			oauthtoken.ByCreatedAt(sql.OrderDesc()),
			oauthtoken.ByID(sql.OrderDesc()),
		).All(ctx)
		if err != nil {
			return nil, err
		}
		applyLatestExpiresAtByClient(statsByClientID, latestTokens)
	}

	finalizeAdminOAuthTokenStatsByClient(statsByClientID)
	return statsByClientID, nil
}

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
