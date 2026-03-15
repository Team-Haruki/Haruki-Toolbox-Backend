package adminoauth

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"net/url"
	"strings"
	"time"

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
	return platformPagination.ParsePageAndPageSize(c, defaultAdminOAuthClientPage, defaultAdminOAuthClientPageSize, maxAdminOAuthClientPageSize)
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
	return &adminOAuthClientStatisticsFilters{From: from, To: to, Bucket: bucket}, nil
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
	return buildAdminOAuthClientTrendPointsFromCounts(from, to, bucket, aggregateTrendCountsFromTimes(authorizationTimes, from, to, bucket), aggregateTrendCountsFromTimes(tokenTimes, from, to, bucket))
}

func buildAdminOAuthClientTrendPointsFromCounts(from, to time.Time, bucket string, authorizationCounts, tokenCounts map[int64]int) []adminOAuthClientTrendPoint {
	points := make([]adminOAuthClientTrendPoint, 0)
	for cursor := truncateTimeByBucket(from.UTC(), bucket); !cursor.After(to.UTC()); cursor = nextTimeBucketStart(cursor, bucket) {
		bucketUnix := cursor.Unix()
		points = append(points, adminOAuthClientTrendPoint{BucketStart: cursor, AuthorizationCreated: authorizationCounts[bucketUnix], TokenIssued: tokenCounts[bucketUnix]})
	}
	return points
}
