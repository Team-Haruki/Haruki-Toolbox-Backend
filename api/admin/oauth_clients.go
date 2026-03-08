package admin

import (
	"encoding/json"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"mime"
	"net/url"
	"regexp"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultAdminOAuthClientStatsWindowHours = 24
	maxAdminOAuthClientStatsWindowHours     = 24 * 30

	defaultAdminOAuthClientTrendBucket = "hour"
	adminOAuthClientTrendBucketHour    = "hour"
	adminOAuthClientTrendBucketDay     = "day"

	adminOAuthClientIDMinLen = 3
	adminOAuthClientIDMaxLen = 128
	adminOAuthClientNameMax  = 128
)

var adminOAuthClientIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type adminOAuthClientUsageStats struct {
	AuthorizationTotal    int        `json:"authorizationTotal"`
	AuthorizationActive   int        `json:"authorizationActive"`
	AuthorizationInWindow int        `json:"authorizationInWindow"`
	TokenTotal            int        `json:"tokenTotal"`
	TokenActive           int        `json:"tokenActive"`
	TokenIssuedInWindow   int        `json:"tokenIssuedInWindow"`
	LatestAuthorizationAt *time.Time `json:"latestAuthorizationAt,omitempty"`
	LatestTokenIssuedAt   *time.Time `json:"latestTokenIssuedAt,omitempty"`
}

type adminOAuthClientListItem struct {
	ClientID     string                     `json:"clientId"`
	Name         string                     `json:"name"`
	ClientType   string                     `json:"clientType"`
	Active       bool                       `json:"active"`
	CreatedAt    time.Time                  `json:"createdAt"`
	RedirectURIs []string                   `json:"redirectUris"`
	Scopes       []string                   `json:"scopes"`
	Usage        adminOAuthClientUsageStats `json:"usage"`
}

type adminOAuthClientListResponse struct {
	GeneratedAt     time.Time                  `json:"generatedAt"`
	WindowHours     int                        `json:"windowHours"`
	WindowStart     time.Time                  `json:"windowStart"`
	WindowEnd       time.Time                  `json:"windowEnd"`
	IncludeInactive bool                       `json:"includeInactive"`
	Total           int                        `json:"total"`
	Items           []adminOAuthClientListItem `json:"items"`
}

type adminOAuthClientActiveResponse struct {
	ClientID string `json:"clientId"`
	Active   bool   `json:"active"`
}

type adminOAuthClientPayload struct {
	ClientID     string   `json:"clientId"`
	Name         string   `json:"name"`
	ClientType   string   `json:"clientType"`
	RedirectURIs []string `json:"redirectUris"`
	Scopes       []string `json:"scopes"`
}

type adminOAuthClientCreateResponse struct {
	ClientID     string    `json:"clientId"`
	ClientSecret string    `json:"clientSecret"`
	Name         string    `json:"name"`
	ClientType   string    `json:"clientType"`
	Active       bool      `json:"active"`
	RedirectURIs []string  `json:"redirectUris"`
	Scopes       []string  `json:"scopes"`
	CreatedAt    time.Time `json:"createdAt"`
}

type adminOAuthClientUpdateResponse struct {
	ClientID     string    `json:"clientId"`
	Name         string    `json:"name"`
	ClientType   string    `json:"clientType"`
	Active       bool      `json:"active"`
	RedirectURIs []string  `json:"redirectUris"`
	Scopes       []string  `json:"scopes"`
	CreatedAt    time.Time `json:"createdAt"`
}

type adminOAuthClientRotateSecretResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type adminOAuthClientDeleteOptions struct {
	RevokeAuthorizations bool `json:"revokeAuthorizations"`
	RevokeTokens         bool `json:"revokeTokens"`
}

type adminOAuthClientDeleteResponse struct {
	ClientID              string `json:"clientId"`
	Active                bool   `json:"active"`
	RevokeAuthorizations  bool   `json:"revokeAuthorizations"`
	RevokeTokens          bool   `json:"revokeTokens"`
	RevokedAuthorizations int    `json:"revokedAuthorizations"`
	RevokedTokens         int    `json:"revokedTokens"`
}

type adminOAuthClientStatisticsFilters struct {
	From   time.Time
	To     time.Time
	Bucket string
}

type adminOAuthClientStatisticsSummary struct {
	AuthorizationTotal          int `json:"authorizationTotal"`
	AuthorizationActive         int `json:"authorizationActive"`
	AuthorizationRevoked        int `json:"authorizationRevoked"`
	AuthorizationCreatedInRange int `json:"authorizationCreatedInRange"`
	TokenTotal                  int `json:"tokenTotal"`
	TokenActive                 int `json:"tokenActive"`
	TokenRevoked                int `json:"tokenRevoked"`
	TokenIssuedInRange          int `json:"tokenIssuedInRange"`
}

type adminOAuthClientTrendPoint struct {
	BucketStart          time.Time `json:"bucketStart"`
	AuthorizationCreated int       `json:"authorizationCreated"`
	TokenIssued          int       `json:"tokenIssued"`
}

type adminOAuthClientStatisticsResponse struct {
	GeneratedAt time.Time                         `json:"generatedAt"`
	ClientID    string                            `json:"clientId"`
	ClientName  string                            `json:"clientName"`
	ClientType  string                            `json:"clientType"`
	Active      bool                              `json:"active"`
	From        time.Time                         `json:"from"`
	To          time.Time                         `json:"to"`
	Bucket      string                            `json:"bucket"`
	Summary     adminOAuthClientStatisticsSummary `json:"summary"`
	Trend       []adminOAuthClientTrendPoint      `json:"trend"`
}

func parseAdminOAuthClientStatsWindowHours(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAdminOAuthClientStatsWindowHours, nil
	}

	hours, err := parsePositiveInt(trimmed, defaultAdminOAuthClientStatsWindowHours, "hours")
	if err != nil {
		return 0, err
	}
	if hours > maxAdminOAuthClientStatsWindowHours {
		return 0, fiber.NewError(fiber.StatusBadRequest, "hours exceeds max range")
	}
	return hours, nil
}

func parseAdminOAuthClientIncludeInactive(raw string) (bool, error) {
	includeInactive, err := parseOptionalBoolField(raw, "include_inactive")
	if err != nil {
		return false, err
	}
	if includeInactive == nil {
		return true, nil
	}
	return *includeInactive, nil
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

func parseAdminOAuthClientActiveFromJSON(body []byte) (*bool, error) {
	var payload struct {
		Active *bool `json:"active"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}
	if payload.Active == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "active is required")
	}
	return payload.Active, nil
}

func parseAdminOAuthClientActiveFromForm(body []byte) (*bool, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}

	active, err := parseOptionalBoolField(values.Get("active"), "active")
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "active is required")
	}
	return active, nil
}

func parseAdminOAuthClientActiveValue(c fiber.Ctx) (bool, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return false, fiber.NewError(fiber.StatusBadRequest, "active is required")
	}

	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			active, err := parseAdminOAuthClientActiveFromJSON(body)
			if err != nil {
				return false, err
			}
			return *active, nil
		}
		if looksLikeFormBody(body) {
			active, err := parseAdminOAuthClientActiveFromForm(body)
			if err != nil {
				return false, err
			}
			return *active, nil
		}
		return false, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return false, fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/json":
		active, err := parseAdminOAuthClientActiveFromJSON(body)
		if err != nil {
			return false, err
		}
		return *active, nil
	case "application/x-www-form-urlencoded":
		active, err := parseAdminOAuthClientActiveFromForm(body)
		if err != nil {
			return false, err
		}
		return *active, nil
	default:
		return false, fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
}

func parseAdminOAuthClientDeleteOptionsFromJSON(body []byte) (adminOAuthClientDeleteOptions, error) {
	options := adminOAuthClientDeleteOptions{
		RevokeAuthorizations: true,
		RevokeTokens:         true,
	}

	var payload struct {
		RevokeAuthorizations      *bool `json:"revokeAuthorizations"`
		RevokeAuthorizationsSnake *bool `json:"revoke_authorizations"`
		RevokeTokens              *bool `json:"revokeTokens"`
		RevokeTokensSnake         *bool `json:"revoke_tokens"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	if payload.RevokeAuthorizations != nil {
		options.RevokeAuthorizations = *payload.RevokeAuthorizations
	} else if payload.RevokeAuthorizationsSnake != nil {
		options.RevokeAuthorizations = *payload.RevokeAuthorizationsSnake
	}
	if payload.RevokeTokens != nil {
		options.RevokeTokens = *payload.RevokeTokens
	} else if payload.RevokeTokensSnake != nil {
		options.RevokeTokens = *payload.RevokeTokensSnake
	}
	return options, nil
}

func parseAdminOAuthClientDeleteOptionsFromForm(body []byte) (adminOAuthClientDeleteOptions, error) {
	options := adminOAuthClientDeleteOptions{
		RevokeAuthorizations: true,
		RevokeTokens:         true,
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}

	revokeAuthorizationsRaw := strings.TrimSpace(values.Get("revoke_authorizations"))
	if revokeAuthorizationsRaw == "" {
		revokeAuthorizationsRaw = strings.TrimSpace(values.Get("revokeAuthorizations"))
	}
	revokeAuthorizations, err := parseOptionalBoolField(revokeAuthorizationsRaw, "revoke_authorizations")
	if err != nil {
		return adminOAuthClientDeleteOptions{}, err
	}
	if revokeAuthorizations != nil {
		options.RevokeAuthorizations = *revokeAuthorizations
	}

	revokeTokensRaw := strings.TrimSpace(values.Get("revoke_tokens"))
	if revokeTokensRaw == "" {
		revokeTokensRaw = strings.TrimSpace(values.Get("revokeTokens"))
	}
	revokeTokens, err := parseOptionalBoolField(revokeTokensRaw, "revoke_tokens")
	if err != nil {
		return adminOAuthClientDeleteOptions{}, err
	}
	if revokeTokens != nil {
		options.RevokeTokens = *revokeTokens
	}
	return options, nil
}

func parseAdminOAuthClientDeleteOptions(c fiber.Ctx) (adminOAuthClientDeleteOptions, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return adminOAuthClientDeleteOptions{
			RevokeAuthorizations: true,
			RevokeTokens:         true,
		}, nil
	}

	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			return parseAdminOAuthClientDeleteOptionsFromJSON(body)
		}
		if looksLikeFormBody(body) {
			return parseAdminOAuthClientDeleteOptionsFromForm(body)
		}
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/json":
		return parseAdminOAuthClientDeleteOptionsFromJSON(body)
	case "application/x-www-form-urlencoded":
		return parseAdminOAuthClientDeleteOptionsFromForm(body)
	default:
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
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

func handleListOAuthClients(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		windowHours, err := parseAdminOAuthClientStatsWindowHours(c.Query("hours"))
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.list", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_hours", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid hours")
		}

		includeInactive, err := parseAdminOAuthClientIncludeInactive(c.Query("include_inactive"))
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.list", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_include_inactive", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid include_inactive")
		}

		now := time.Now().UTC()
		windowStart := now.Add(-time.Duration(windowHours) * time.Hour)

		clientQuery := apiHelper.DBManager.DB.OAuthClient.Query()
		if !includeInactive {
			clientQuery = clientQuery.Where(oauthclient.ActiveEQ(true))
		}

		clients, err := clientQuery.Order(
			oauthclient.ByCreatedAt(sql.OrderDesc()),
			oauthclient.ByID(sql.OrderDesc()),
		).All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.list", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_clients_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth clients")
		}

		items := make([]adminOAuthClientListItem, 0, len(clients))
		for _, client := range clients {
			usage, usageErr := queryAdminOAuthClientUsageStats(c, apiHelper, client.ID, windowStart)
			if usageErr != nil {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.list", "oauth_client", client.ClientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_usage_stats_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client usage")
			}

			items = append(items, adminOAuthClientListItem{
				ClientID:     client.ClientID,
				Name:         client.Name,
				ClientType:   client.ClientType,
				Active:       client.Active,
				CreatedAt:    client.CreatedAt.UTC(),
				RedirectURIs: append([]string(nil), client.RedirectUris...),
				Scopes:       append([]string(nil), client.Scopes...),
				Usage:        usage,
			})
		}

		resp := adminOAuthClientListResponse{
			GeneratedAt:     now,
			WindowHours:     windowHours,
			WindowStart:     windowStart,
			WindowEnd:       now,
			IncludeInactive: includeInactive,
			Total:           len(items),
			Items:           items,
		}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.list", "oauth_client", "all", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"windowHours":     windowHours,
			"includeInactive": includeInactive,
			"clientCount":     len(items),
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, _, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.create", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		payload, err := parseAdminOAuthClientPayload(c, true)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.create", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		plainSecret, hashedSecret, err := generateAdminOAuthClientSecret()
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.create", "oauth_client", payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("generate_client_secret_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to generate client secret")
		}

		createdClient, err := apiHelper.DBManager.DB.OAuthClient.Create().
			SetClientID(payload.ClientID).
			SetClientSecret(hashedSecret).
			SetName(payload.Name).
			SetClientType(payload.ClientType).
			SetRedirectUris(payload.RedirectURIs).
			SetScopes(payload.Scopes).
			SetActive(true).
			Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.create", "oauth_client", payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_id_conflict", nil))
				return harukiAPIHelper.ErrorBadRequest(c, "clientId already exists")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.create", "oauth_client", payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create oauth client")
		}

		resp := adminOAuthClientCreateResponse{
			ClientID:     createdClient.ClientID,
			ClientSecret: plainSecret,
			Name:         createdClient.Name,
			ClientType:   createdClient.ClientType,
			Active:       createdClient.Active,
			RedirectURIs: append([]string(nil), createdClient.RedirectUris...),
			Scopes:       append([]string(nil), createdClient.Scopes...),
			CreatedAt:    createdClient.CreatedAt.UTC(),
		}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.create", "oauth_client", createdClient.ClientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"clientType":  createdClient.ClientType,
			"scopeCount":  len(createdClient.Scopes),
			"redirectCnt": len(createdClient.RedirectUris),
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client created", &resp)
	}
}

func handleUpdateOAuthClientActive(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.active.update", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.active.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		active, err := parseAdminOAuthClientActiveValue(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.active.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.active.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.active.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		updatedClient, err := apiHelper.DBManager.DB.OAuthClient.UpdateOneID(dbClient.ID).
			SetActive(active).
			Save(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.active.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}

		resp := adminOAuthClientActiveResponse{
			ClientID: updatedClient.ClientID,
			Active:   updatedClient.Active,
		}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.active.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"active": active,
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client status updated", &resp)
	}
}

func handleUpdateOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.update", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		payload, err := parseAdminOAuthClientPayload(c, false)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		updatedClient, err := apiHelper.DBManager.DB.OAuthClient.UpdateOneID(dbClient.ID).
			SetName(payload.Name).
			SetClientType(payload.ClientType).
			SetRedirectUris(payload.RedirectURIs).
			SetScopes(payload.Scopes).
			Save(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}

		resp := adminOAuthClientUpdateResponse{
			ClientID:     updatedClient.ClientID,
			Name:         updatedClient.Name,
			ClientType:   updatedClient.ClientType,
			Active:       updatedClient.Active,
			RedirectURIs: append([]string(nil), updatedClient.RedirectUris...),
			Scopes:       append([]string(nil), updatedClient.Scopes...),
			CreatedAt:    updatedClient.CreatedAt.UTC(),
		}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.update", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"clientType":  updatedClient.ClientType,
			"scopeCount":  len(updatedClient.Scopes),
			"redirectCnt": len(updatedClient.RedirectUris),
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client updated", &resp)
	}
}

func handleRotateOAuthClientSecret(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.rotate_secret", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.rotate_secret", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.rotate_secret", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.rotate_secret", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		plainSecret, hashedSecret, err := generateAdminOAuthClientSecret()
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.rotate_secret", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("generate_client_secret_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to generate client secret")
		}

		if _, err := apiHelper.DBManager.DB.OAuthClient.UpdateOneID(dbClient.ID).
			SetClientSecret(hashedSecret).
			Save(c.Context()); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.rotate_secret", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_client_secret_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to rotate oauth client secret")
		}

		resp := adminOAuthClientRotateSecretResponse{
			ClientID:     dbClient.ClientID,
			ClientSecret: plainSecret,
		}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.rotate_secret", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "oauth client secret rotated", &resp)
	}
}

func handleDeleteOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		options, err := parseAdminOAuthClientDeleteOptions(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("start_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to start transaction")
		}

		if _, err := tx.OAuthClient.UpdateOneID(dbClient.ID).
			SetActive(false).
			Save(c.Context()); err != nil {
			_ = tx.Rollback()
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("disable_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to disable oauth client")
		}

		revokedAuthorizations := 0
		if options.RevokeAuthorizations {
			revokedAuthorizations, err = tx.OAuthAuthorization.Update().
				Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(dbClient.ID))).
				SetRevoked(true).
				Save(c.Context())
			if err != nil {
				_ = tx.Rollback()
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("revoke_authorizations_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
			}
		}

		revokedTokens := 0
		if options.RevokeTokens {
			revokedTokens, err = tx.OAuthToken.Update().
				Where(oauthtoken.HasClientWith(oauthclient.IDEQ(dbClient.ID))).
				SetRevoked(true).
				Save(c.Context())
			if err != nil {
				_ = tx.Rollback()
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("revoke_tokens_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth tokens")
			}
		}

		if err := tx.Commit(); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("commit_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to commit oauth client delete")
		}

		resp := adminOAuthClientDeleteResponse{
			ClientID:              dbClient.ClientID,
			Active:                false,
			RevokeAuthorizations:  options.RevokeAuthorizations,
			RevokeTokens:          options.RevokeTokens,
			RevokedAuthorizations: revokedAuthorizations,
			RevokedTokens:         revokedTokens,
		}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.delete", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"revokeAuthorizations":  options.RevokeAuthorizations,
			"revokeTokens":          options.RevokeTokens,
			"revokedAuthorizations": revokedAuthorizations,
			"revokedTokens":         revokedTokens,
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client disabled", &resp)
	}
}

func handleGetOAuthClientStatistics(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		filters, err := parseAdminOAuthClientStatisticsFilters(c, time.Now())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_query_filters", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		authorizationBase := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(dbClient.ID)))
		authorizationTotal, err := authorizationBase.Clone().Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_authorizations_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth authorizations")
		}
		authorizationActive, err := authorizationBase.Clone().Where(oauthauthorization.RevokedEQ(false)).Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_active_authorizations_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count active oauth authorizations")
		}
		if authorizationActive > authorizationTotal {
			authorizationActive = authorizationTotal
		}
		authorizationRevoked := authorizationTotal - authorizationActive

		tokenBase := apiHelper.DBManager.DB.OAuthToken.Query().
			Where(oauthtoken.HasClientWith(oauthclient.IDEQ(dbClient.ID)))
		tokenTotal, err := tokenBase.Clone().Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_tokens_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth tokens")
		}
		tokenActive, err := tokenBase.Clone().Where(oauthtoken.RevokedEQ(false)).Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_active_tokens_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count active oauth tokens")
		}
		if tokenActive > tokenTotal {
			tokenActive = tokenTotal
		}
		tokenRevoked := tokenTotal - tokenActive

		authorizationRows, err := authorizationBase.Clone().
			Where(
				oauthauthorization.CreatedAtGTE(filters.From),
				oauthauthorization.CreatedAtLTE(filters.To),
			).
			Select(oauthauthorization.FieldCreatedAt).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_authorization_trends_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth authorization trends")
		}

		tokenRows, err := tokenBase.Clone().
			Where(
				oauthtoken.CreatedAtGTE(filters.From),
				oauthtoken.CreatedAtLTE(filters.To),
			).
			Select(oauthtoken.FieldCreatedAt).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_token_trends_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth token trends")
		}

		authorizationTimes := make([]time.Time, 0, len(authorizationRows))
		for _, row := range authorizationRows {
			authorizationTimes = append(authorizationTimes, row.CreatedAt.UTC())
		}
		tokenTimes := make([]time.Time, 0, len(tokenRows))
		for _, row := range tokenRows {
			tokenTimes = append(tokenTimes, row.CreatedAt.UTC())
		}

		resp := adminOAuthClientStatisticsResponse{
			GeneratedAt: time.Now().UTC(),
			ClientID:    dbClient.ClientID,
			ClientName:  dbClient.Name,
			ClientType:  dbClient.ClientType,
			Active:      dbClient.Active,
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Bucket:      filters.Bucket,
			Summary: adminOAuthClientStatisticsSummary{
				AuthorizationTotal:          authorizationTotal,
				AuthorizationActive:         authorizationActive,
				AuthorizationRevoked:        authorizationRevoked,
				AuthorizationCreatedInRange: len(authorizationTimes),
				TokenTotal:                  tokenTotal,
				TokenActive:                 tokenActive,
				TokenRevoked:                tokenRevoked,
				TokenIssuedInRange:          len(tokenTimes),
			},
			Trend: buildAdminOAuthClientTrendPoints(filters.From, filters.To, filters.Bucket, authorizationTimes, tokenTimes),
		}

		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.statistics.query", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"from":        resp.From.Format(time.RFC3339),
			"to":          resp.To.Format(time.RFC3339),
			"bucket":      resp.Bucket,
			"trendPoints": len(resp.Trend),
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func registerAdminOAuthClientRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	oauthClients := adminGroup.Group("/oauth-clients", RequireAdmin(apiHelper))
	oauthClients.Post("/", RequireSuperAdmin(apiHelper), handleCreateOAuthClient(apiHelper))
	oauthClients.Get("/", handleListOAuthClients(apiHelper))
	oauthClients.Get("/:client_id/authorizations", handleListOAuthClientAuthorizations(apiHelper))
	oauthClients.Get("/:client_id/statistics", handleGetOAuthClientStatistics(apiHelper))
	oauthClients.Get("/:client_id/audit-logs", handleListOAuthClientAuditLogs(apiHelper))
	oauthClients.Get("/:client_id/audit-summary", handleGetOAuthClientAuditSummary(apiHelper))
	oauthClients.Post("/:client_id/revoke", RequireSuperAdmin(apiHelper), handleRevokeOAuthClient(apiHelper))
	oauthClients.Post("/:client_id/restore", RequireSuperAdmin(apiHelper), handleRestoreOAuthClient(apiHelper))
	oauthClients.Put("/:client_id", RequireSuperAdmin(apiHelper), handleUpdateOAuthClient(apiHelper))
	oauthClients.Put("/:client_id/active", handleUpdateOAuthClientActive(apiHelper))
	oauthClients.Post("/:client_id/rotate-secret", RequireSuperAdmin(apiHelper), handleRotateOAuthClientSecret(apiHelper))
	oauthClients.Delete("/:client_id", RequireSuperAdmin(apiHelper), handleDeleteOAuthClient(apiHelper))
}
