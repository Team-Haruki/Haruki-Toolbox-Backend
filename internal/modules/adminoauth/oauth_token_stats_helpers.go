package adminoauth

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

type adminOAuthTokenStats struct {
	Total           int        `json:"total"`
	Active          int        `json:"active"`
	Revoked         int        `json:"revoked"`
	LatestIssuedAt  *time.Time `json:"latestIssuedAt,omitempty"`
	LatestExpiresAt *time.Time `json:"latestExpiresAt,omitempty"`
}

func queryOAuthClientTokenStatsByUsers(ctx context.Context, db *postgresql.Client, clientDBID int, userIDs []string) (map[string]adminOAuthTokenStats, error) {
	if clientDBID <= 0 || len(userIDs) == 0 {
		return map[string]adminOAuthTokenStats{}, nil
	}

	unique := make([]string, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		unique = append(unique, userID)
	}
	if len(unique) == 0 {
		return map[string]adminOAuthTokenStats{}, nil
	}

	statsByUserID := make(map[string]adminOAuthTokenStats, len(unique))
	for _, userID := range unique {
		statsByUserID[userID] = adminOAuthTokenStats{}
	}

	baseQuery := db.OAuthToken.Query().Where(
		oauthtoken.HasClientWith(oauthclient.IDEQ(clientDBID)),
		oauthtoken.HasUserWith(userSchema.IDIn(unique...)),
	)

	var totalRows []struct {
		UserID string `json:"user_oauth_tokens"`
		Count  int    `json:"count"`
	}
	if err := baseQuery.Clone().
		GroupBy(oauthtoken.UserColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &totalRows); err != nil {
		return nil, err
	}
	for _, row := range totalRows {
		stats := statsByUserID[row.UserID]
		stats.Total = row.Count
		statsByUserID[row.UserID] = stats
	}

	var activeRows []struct {
		UserID string `json:"user_oauth_tokens"`
		Count  int    `json:"count"`
	}
	if err := baseQuery.Clone().
		Where(oauthtoken.RevokedEQ(false)).
		GroupBy(oauthtoken.UserColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &activeRows); err != nil {
		return nil, err
	}
	for _, row := range activeRows {
		stats := statsByUserID[row.UserID]
		stats.Active = row.Count
		statsByUserID[row.UserID] = stats
	}

	var latestIssuedRows []struct {
		UserID         string    `json:"user_oauth_tokens"`
		LatestIssuedAt time.Time `json:"latest_issued_at"`
	}
	if err := baseQuery.Clone().
		GroupBy(oauthtoken.UserColumn).
		Aggregate(postgresql.As(postgresql.Max(oauthtoken.FieldCreatedAt), "latest_issued_at")).
		Scan(ctx, &latestIssuedRows); err != nil {
		return nil, err
	}

	latestPredicates := make([]predicate.OAuthToken, 0, len(latestIssuedRows))
	for _, row := range latestIssuedRows {
		stats := statsByUserID[row.UserID]
		issuedAt := row.LatestIssuedAt.UTC()
		stats.LatestIssuedAt = &issuedAt
		statsByUserID[row.UserID] = stats

		latestPredicates = append(latestPredicates, oauthtoken.And(
			oauthtoken.HasUserWith(userSchema.IDEQ(row.UserID)),
			oauthtoken.CreatedAtEQ(row.LatestIssuedAt),
		))
	}

	if len(latestPredicates) > 0 {
		latestTokens, err := db.OAuthToken.Query().Where(
			oauthtoken.HasClientWith(oauthclient.IDEQ(clientDBID)),
			oauthtoken.Or(latestPredicates...),
		).WithUser().Order(
			oauthtoken.ByCreatedAt(sql.OrderDesc()),
			oauthtoken.ByID(sql.OrderDesc()),
		).All(ctx)
		if err != nil {
			return nil, err
		}
		applyLatestExpiresAtByUser(statsByUserID, latestTokens)
	}

	finalizeAdminOAuthTokenStatsByUser(statsByUserID)
	return statsByUserID, nil
}

func applyLatestExpiresAtByUser(statsByUserID map[string]adminOAuthTokenStats, rows []*postgresql.OAuthToken) {
	seenLatestByUserID := make(map[string]struct{}, len(statsByUserID))
	for _, row := range rows {
		if row == nil || row.Edges.User == nil {
			continue
		}
		userID := row.Edges.User.ID
		if _, seen := seenLatestByUserID[userID]; seen {
			continue
		}
		seenLatestByUserID[userID] = struct{}{}
		if row.ExpiresAt == nil {
			continue
		}
		stats := statsByUserID[userID]
		expiresAt := row.ExpiresAt.UTC()
		stats.LatestExpiresAt = &expiresAt
		statsByUserID[userID] = stats
	}
}

func finalizeAdminOAuthTokenStatsByUser(statsByUserID map[string]adminOAuthTokenStats) {
	for userID, stats := range statsByUserID {
		if stats.Active > stats.Total {
			stats.Active = stats.Total
		}
		stats.Revoked = stats.Total - stats.Active
		statsByUserID[userID] = stats
	}
}
