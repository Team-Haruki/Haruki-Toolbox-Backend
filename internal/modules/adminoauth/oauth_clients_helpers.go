package adminoauth

import (
	"context"
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"net/url"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

func parseAdminOAuthClientStatsWindowHours(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAdminOAuthClientStatsWindowHours, nil
	}

	hours, err := platformPagination.ParsePositiveInt(trimmed, defaultAdminOAuthClientStatsWindowHours, "hours")
	if err != nil {
		return 0, err
	}
	if hours > maxAdminOAuthClientStatsWindowHours {
		return 0, fiber.NewError(fiber.StatusBadRequest, "hours exceeds max range")
	}
	return hours, nil
}

func parseAdminOAuthClientIncludeInactive(raw string) (bool, error) {
	includeInactive, err := adminCoreModule.ParseOptionalBoolField(raw, "include_inactive")
	if err != nil {
		return false, err
	}
	if includeInactive == nil {
		return true, nil
	}
	return *includeInactive, nil
}

func parseAdminOAuthClientListPagination(c fiber.Ctx) (int, int, error) {
	return platformPagination.ParsePageAndPageSize(
		c,
		defaultAdminOAuthClientPage,
		defaultAdminOAuthClientPageSize,
		maxAdminOAuthClientPageSize,
	)
}

func parseAdminOAuthClientTrendBucket(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return defaultAdminOAuthClientTrendBucket, nil
	}

	switch trimmed {
	case adminOAuthClientTrendBucketHour, adminOAuthClientTrendBucketDay:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid bucket")
	}
}

func normalizeAdminOAuthClientType(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "public", "confidential":
		return normalized, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid clientType")
	}
}

func sanitizeAdminOAuthClientID(raw string) (string, error) {
	clientID := strings.TrimSpace(raw)
	if clientID == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "clientId is required")
	}
	if len(clientID) < adminOAuthClientIDMinLen || len(clientID) > adminOAuthClientIDMaxLen {
		return "", fiber.NewError(fiber.StatusBadRequest, "clientId length is invalid")
	}
	if !adminOAuthClientIDPattern.MatchString(clientID) {
		return "", fiber.NewError(fiber.StatusBadRequest, "clientId contains invalid characters")
	}
	return clientID, nil
}

func sanitizeAdminOAuthClientName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	if len(name) > adminOAuthClientNameMax {
		return "", fiber.NewError(fiber.StatusBadRequest, "name exceeds max length")
	}
	return name, nil
}

func sanitizeAdminOAuthClientRedirectURIs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "redirectUris is required")
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		redirectURI := strings.TrimSpace(raw)
		if redirectURI == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, "redirectUris contains empty value")
		}
		if strings.Contains(redirectURI, "#") {
			return nil, fiber.NewError(fiber.StatusBadRequest, "redirectUris must not include fragment")
		}

		parsed, err := url.ParseRequestURI(redirectURI)
		if err != nil || strings.TrimSpace(parsed.Scheme) == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, "redirectUris contains invalid uri")
		}

		if _, ok := seen[redirectURI]; ok {
			continue
		}
		seen[redirectURI] = struct{}{}
		result = append(result, redirectURI)
	}
	if len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "redirectUris is required")
	}
	return result, nil
}

func sanitizeAdminOAuthClientScopes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "scopes is required")
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, "scopes contains empty value")
		}
		if _, ok := harukiOAuth2.AllScopes[scope]; !ok {
			return nil, fiber.NewError(fiber.StatusBadRequest, "scopes contains invalid scope")
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	if len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "scopes is required")
	}
	return result, nil
}

func parseAdminOAuthClientPayload(c fiber.Ctx, requireClientID bool) (*adminOAuthClientPayload, error) {
	var payload adminOAuthClientPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	if requireClientID {
		clientID, err := sanitizeAdminOAuthClientID(payload.ClientID)
		if err != nil {
			return nil, err
		}
		payload.ClientID = clientID
	}

	name, err := sanitizeAdminOAuthClientName(payload.Name)
	if err != nil {
		return nil, err
	}
	clientType, err := normalizeAdminOAuthClientType(payload.ClientType)
	if err != nil {
		return nil, err
	}
	redirectURIs, err := sanitizeAdminOAuthClientRedirectURIs(payload.RedirectURIs)
	if err != nil {
		return nil, err
	}
	scopes, err := sanitizeAdminOAuthClientScopes(payload.Scopes)
	if err != nil {
		return nil, err
	}

	payload.Name = name
	payload.ClientType = clientType
	payload.RedirectURIs = redirectURIs
	payload.Scopes = scopes
	return &payload, nil
}

func generateAdminOAuthClientSecret() (plainSecret string, hashedSecret string, err error) {
	plainSecret, err = harukiOAuth2.GenerateRandomToken(32)
	if err != nil {
		return "", "", err
	}
	secretHash, err := bcrypt.GenerateFromPassword([]byte(plainSecret), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	return plainSecret, string(secretHash), nil
}

func parseAdminOAuthClientStatisticsFilters(c fiber.Ctx, now time.Time) (*adminOAuthClientStatisticsFilters, error) {
	from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}

	bucket, err := parseAdminOAuthClientTrendBucket(c.Query("bucket"))
	if err != nil {
		return nil, err
	}

	return &adminOAuthClientStatisticsFilters{
		From:   from,
		To:     to,
		Bucket: bucket,
	}, nil
}

func truncateTimeByBucket(t time.Time, bucket string) time.Time {
	t = t.UTC()
	switch bucket {
	case adminOAuthClientTrendBucketDay:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	default:
		return t.Truncate(time.Hour)
	}
}

func nextTimeBucketStart(t time.Time, bucket string) time.Time {
	t = t.UTC()
	switch bucket {
	case adminOAuthClientTrendBucketDay:
		return t.AddDate(0, 0, 1)
	default:
		return t.Add(time.Hour)
	}
}

func buildAdminOAuthClientTrendPoints(from, to time.Time, bucket string, authorizationTimes []time.Time, tokenTimes []time.Time) []adminOAuthClientTrendPoint {
	from = from.UTC()
	to = to.UTC()
	if to.Before(from) {
		return nil
	}

	authorizationCounts := make(map[int64]int)
	for _, eventTime := range authorizationTimes {
		eventTime = eventTime.UTC()
		if eventTime.Before(from) || eventTime.After(to) {
			continue
		}
		bucketStart := truncateTimeByBucket(eventTime, bucket)
		authorizationCounts[bucketStart.Unix()]++
	}

	tokenCounts := make(map[int64]int)
	for _, eventTime := range tokenTimes {
		eventTime = eventTime.UTC()
		if eventTime.Before(from) || eventTime.After(to) {
			continue
		}
		bucketStart := truncateTimeByBucket(eventTime, bucket)
		tokenCounts[bucketStart.Unix()]++
	}

	return buildAdminOAuthClientTrendPointsFromCounts(from, to, bucket, authorizationCounts, tokenCounts)
}

func buildAdminOAuthClientTrendPointsFromCounts(from, to time.Time, bucket string, authorizationCounts, tokenCounts map[int64]int) []adminOAuthClientTrendPoint {
	from = from.UTC()
	to = to.UTC()
	if to.Before(from) {
		return nil
	}

	points := make([]adminOAuthClientTrendPoint, 0)
	for cursor := truncateTimeByBucket(from, bucket); !cursor.After(to); cursor = nextTimeBucketStart(cursor, bucket) {
		key := cursor.Unix()
		points = append(points, adminOAuthClientTrendPoint{
			BucketStart:          cursor,
			AuthorizationCreated: authorizationCounts[key],
			TokenIssued:          tokenCounts[key],
		})
	}
	return points
}

func queryAdminOAuthClientUsageStatsByClients(ctx context.Context, db *postgresql.Client, clientDBIDs []int, windowStart time.Time) (map[int]adminOAuthClientUsageStats, error) {
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
		return map[int]adminOAuthClientUsageStats{}, nil
	}

	statsByClientID := make(map[int]adminOAuthClientUsageStats, len(unique))
	for _, clientDBID := range unique {
		statsByClientID[clientDBID] = adminOAuthClientUsageStats{}
	}

	authBase := db.OAuthAuthorization.Query().
		Where(oauthauthorization.HasClientWith(oauthclient.IDIn(unique...)))

	var authorizationTotalRows []struct {
		ClientID int `json:"oauth_client_authorizations"`
		Count    int `json:"count"`
	}
	if err := authBase.Clone().
		GroupBy(oauthauthorization.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &authorizationTotalRows); err != nil {
		return nil, err
	}
	for _, row := range authorizationTotalRows {
		stats := statsByClientID[row.ClientID]
		stats.AuthorizationTotal = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var authorizationActiveRows []struct {
		ClientID int `json:"oauth_client_authorizations"`
		Count    int `json:"count"`
	}
	if err := authBase.Clone().
		Where(oauthauthorization.RevokedEQ(false)).
		GroupBy(oauthauthorization.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &authorizationActiveRows); err != nil {
		return nil, err
	}
	for _, row := range authorizationActiveRows {
		stats := statsByClientID[row.ClientID]
		stats.AuthorizationActive = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var authorizationInWindowRows []struct {
		ClientID int `json:"oauth_client_authorizations"`
		Count    int `json:"count"`
	}
	if err := authBase.Clone().
		Where(oauthauthorization.CreatedAtGTE(windowStart)).
		GroupBy(oauthauthorization.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &authorizationInWindowRows); err != nil {
		return nil, err
	}
	for _, row := range authorizationInWindowRows {
		stats := statsByClientID[row.ClientID]
		stats.AuthorizationInWindow = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var latestAuthorizationRows []struct {
		ClientID              int       `json:"oauth_client_authorizations"`
		LatestAuthorizationAt time.Time `json:"latest_authorization_at"`
	}
	if err := authBase.Clone().
		GroupBy(oauthauthorization.ClientColumn).
		Aggregate(postgresql.As(postgresql.Max(oauthauthorization.FieldCreatedAt), "latest_authorization_at")).
		Scan(ctx, &latestAuthorizationRows); err != nil {
		return nil, err
	}
	for _, row := range latestAuthorizationRows {
		stats := statsByClientID[row.ClientID]
		latestAuthorizationAt := row.LatestAuthorizationAt.UTC()
		stats.LatestAuthorizationAt = &latestAuthorizationAt
		statsByClientID[row.ClientID] = stats
	}

	tokenBase := db.OAuthToken.Query().
		Where(oauthtoken.HasClientWith(oauthclient.IDIn(unique...)))

	var tokenTotalRows []struct {
		ClientID int `json:"oauth_client_tokens"`
		Count    int `json:"count"`
	}
	if err := tokenBase.Clone().
		GroupBy(oauthtoken.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &tokenTotalRows); err != nil {
		return nil, err
	}
	for _, row := range tokenTotalRows {
		stats := statsByClientID[row.ClientID]
		stats.TokenTotal = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var tokenActiveRows []struct {
		ClientID int `json:"oauth_client_tokens"`
		Count    int `json:"count"`
	}
	if err := tokenBase.Clone().
		Where(oauthtoken.RevokedEQ(false)).
		GroupBy(oauthtoken.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &tokenActiveRows); err != nil {
		return nil, err
	}
	for _, row := range tokenActiveRows {
		stats := statsByClientID[row.ClientID]
		stats.TokenActive = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var tokenInWindowRows []struct {
		ClientID int `json:"oauth_client_tokens"`
		Count    int `json:"count"`
	}
	if err := tokenBase.Clone().
		Where(oauthtoken.CreatedAtGTE(windowStart)).
		GroupBy(oauthtoken.ClientColumn).
		Aggregate(postgresql.As(postgresql.Count(), "count")).
		Scan(ctx, &tokenInWindowRows); err != nil {
		return nil, err
	}
	for _, row := range tokenInWindowRows {
		stats := statsByClientID[row.ClientID]
		stats.TokenIssuedInWindow = row.Count
		statsByClientID[row.ClientID] = stats
	}

	var latestTokenRows []struct {
		ClientID          int       `json:"oauth_client_tokens"`
		LatestTokenIssued time.Time `json:"latest_token_issued_at"`
	}
	if err := tokenBase.Clone().
		GroupBy(oauthtoken.ClientColumn).
		Aggregate(postgresql.As(postgresql.Max(oauthtoken.FieldCreatedAt), "latest_token_issued_at")).
		Scan(ctx, &latestTokenRows); err != nil {
		return nil, err
	}
	for _, row := range latestTokenRows {
		stats := statsByClientID[row.ClientID]
		latestTokenIssuedAt := row.LatestTokenIssued.UTC()
		stats.LatestTokenIssuedAt = &latestTokenIssuedAt
		statsByClientID[row.ClientID] = stats
	}

	for clientDBID, stats := range statsByClientID {
		if stats.AuthorizationActive > stats.AuthorizationTotal {
			stats.AuthorizationActive = stats.AuthorizationTotal
		}
		if stats.TokenActive > stats.TokenTotal {
			stats.TokenActive = stats.TokenTotal
		}
		statsByClientID[clientDBID] = stats
	}

	return statsByClientID, nil
}

func queryAdminOAuthClientUsageStats(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientDBID int, windowStart time.Time) (adminOAuthClientUsageStats, error) {
	authBase := apiHelper.DBManager.DB.OAuthAuthorization.Query().
		Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(clientDBID)))
	authorizationTotal, err := authBase.Clone().Count(c.Context())
	if err != nil {
		return adminOAuthClientUsageStats{}, err
	}
	authorizationActive, err := authBase.Clone().Where(oauthauthorization.RevokedEQ(false)).Count(c.Context())
	if err != nil {
		return adminOAuthClientUsageStats{}, err
	}
	authorizationInWindow, err := authBase.Clone().Where(oauthauthorization.CreatedAtGTE(windowStart)).Count(c.Context())
	if err != nil {
		return adminOAuthClientUsageStats{}, err
	}

	tokenBase := apiHelper.DBManager.DB.OAuthToken.Query().
		Where(oauthtoken.HasClientWith(oauthclient.IDEQ(clientDBID)))
	tokenTotal, err := tokenBase.Clone().Count(c.Context())
	if err != nil {
		return adminOAuthClientUsageStats{}, err
	}
	tokenActive, err := tokenBase.Clone().Where(oauthtoken.RevokedEQ(false)).Count(c.Context())
	if err != nil {
		return adminOAuthClientUsageStats{}, err
	}
	tokenIssuedInWindow, err := tokenBase.Clone().Where(oauthtoken.CreatedAtGTE(windowStart)).Count(c.Context())
	if err != nil {
		return adminOAuthClientUsageStats{}, err
	}

	if authorizationActive > authorizationTotal {
		authorizationActive = authorizationTotal
	}
	if tokenActive > tokenTotal {
		tokenActive = tokenTotal
	}

	stats := adminOAuthClientUsageStats{
		AuthorizationTotal:    authorizationTotal,
		AuthorizationActive:   authorizationActive,
		AuthorizationInWindow: authorizationInWindow,
		TokenTotal:            tokenTotal,
		TokenActive:           tokenActive,
		TokenIssuedInWindow:   tokenIssuedInWindow,
	}

	latestAuthorization, err := authBase.Clone().Order(
		oauthauthorization.ByCreatedAt(sql.OrderDesc()),
		oauthauthorization.ByID(sql.OrderDesc()),
	).First(c.Context())
	if err != nil {
		if !postgresql.IsNotFound(err) {
			return adminOAuthClientUsageStats{}, err
		}
	} else {
		latestAuthorizationAt := latestAuthorization.CreatedAt.UTC()
		stats.LatestAuthorizationAt = &latestAuthorizationAt
	}

	latestToken, err := tokenBase.Clone().Order(
		oauthtoken.ByCreatedAt(sql.OrderDesc()),
		oauthtoken.ByID(sql.OrderDesc()),
	).First(c.Context())
	if err != nil {
		if !postgresql.IsNotFound(err) {
			return adminOAuthClientUsageStats{}, err
		}
	} else {
		latestTokenIssuedAt := latestToken.CreatedAt.UTC()
		stats.LatestTokenIssuedAt = &latestTokenIssuedAt
	}

	return stats, nil
}
