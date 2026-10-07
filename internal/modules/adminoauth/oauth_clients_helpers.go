package adminoauth

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"

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

// sanitizeAdminOAuthClientRedirectURIs accepts an empty list: whether redirect
// URIs are required depends on the effective grant types, which only the handler
// knows (validateAdminOAuthClientGrantRules).
func sanitizeAdminOAuthClientRedirectURIs(values []string) ([]string, error) {
	return sanitizeAdminOAuthClientURIs(values, "redirectUris")
}

// sanitizeAdminOAuthClientPostLogoutRedirectURIs keeps nil as nil so an update
// that omits the field leaves the registered list alone.
func sanitizeAdminOAuthClientPostLogoutRedirectURIs(values []string, redirectURIs []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	result, err := sanitizeAdminOAuthClientURIs(values, "postLogoutRedirectUris")
	if err != nil {
		return nil, err
	}
	if err := ensureAdminOAuthClientPostLogoutRedirectURIsMatch(result, redirectURIs); err != nil {
		return nil, err
	}
	return result, nil
}

// ensureAdminOAuthClientKeptPostLogoutRedirectURIsMatch checks the registered
// post-logout URIs an update keeps (payload omitted the field) against the new
// redirect URIs. With no redirect URIs left, UpdateHydraOAuthClient clears the
// kept list in the same patch (device-only clients), so nothing is rejected.
func ensureAdminOAuthClientKeptPostLogoutRedirectURIsMatch(kept []string, redirectURIs []string) error {
	if len(redirectURIs) == 0 {
		return nil
	}
	return ensureAdminOAuthClientPostLogoutRedirectURIsMatch(kept, redirectURIs)
}

// ensureAdminOAuthClientPostLogoutRedirectURIsMatch applies Hydra's own rule:
// each post-logout URI shares scheme, host and port with a redirect URI. Hydra
// rejects the write otherwise, so checking here turns that into a 400.
func ensureAdminOAuthClientPostLogoutRedirectURIsMatch(postLogoutRedirectURIs []string, redirectURIs []string) error {
	if len(postLogoutRedirectURIs) == 0 {
		return nil
	}
	if len(redirectURIs) == 0 {
		// Hydra refuses post-logout URIs on a client without redirect URIs, which
		// is what a device-only client usually is.
		return &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodePostLogoutRequiresRedirectURIs, Message: "postLogoutRedirectUris requires redirectUris"}
	}
	type uriOrigin struct{ scheme, host, port string }
	origins := make(map[uriOrigin]struct{}, len(redirectURIs))
	for _, redirectURI := range redirectURIs {
		if parsed, err := url.ParseRequestURI(redirectURI); err == nil {
			origins[uriOrigin{parsed.Scheme, parsed.Hostname(), parsed.Port()}] = struct{}{}
		}
	}
	for _, postLogoutRedirectURI := range postLogoutRedirectURIs {
		parsed, err := url.ParseRequestURI(postLogoutRedirectURI)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "postLogoutRedirectUris contains invalid uri")
		}
		if _, ok := origins[uriOrigin{parsed.Scheme, parsed.Hostname(), parsed.Port()}]; !ok {
			return fiber.NewError(fiber.StatusBadRequest, "postLogoutRedirectUris must match the scheme, host and port of a redirect uri")
		}
	}
	return nil
}

func sanitizeAdminOAuthClientURIs(values []string, field string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		uri := strings.TrimSpace(raw)
		if uri == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, field+" contains empty value")
		}
		if strings.Contains(uri, "#") {
			return nil, fiber.NewError(fiber.StatusBadRequest, field+" must not include fragment")
		}
		parsed, err := url.ParseRequestURI(uri)
		if err != nil || strings.TrimSpace(parsed.Scheme) == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, field+" contains invalid uri")
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		result = append(result, uri)
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
	if _, hasOpenID := seen[harukiOAuth2.ScopeOpenID]; !hasOpenID {
		if _, hasProfile := seen[harukiOAuth2.ScopeProfile]; hasProfile {
			return nil, fiber.NewError(fiber.StatusBadRequest, "profile scope requires openid")
		}
		if _, hasEmail := seen[harukiOAuth2.ScopeEmail]; hasEmail {
			return nil, fiber.NewError(fiber.StatusBadRequest, "email scope requires openid")
		}
	}
	return result, nil
}

// sanitizeAdminOAuthClientGrantTypes keeps nil as nil (keep on update, default on
// create). Any other list must name only grants the admin form manages and hold
// authorization_code or the device grant; refresh_token alone is therefore
// refused too.
func sanitizeAdminOAuthClientGrantTypes(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	result := make([]string, 0, len(values))
	for _, raw := range values {
		grantType := strings.TrimSpace(raw)
		switch grantType {
		case oauth2Module.HydraGrantTypeAuthorizationCode, oauth2Module.HydraGrantTypeRefreshToken, oauth2Module.HydraGrantTypeDeviceCode:
		default:
			return nil, &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodeUnsupportedGrantType, Message: "grantTypes contains unsupported grant type"}
		}
		if !slices.Contains(result, grantType) {
			result = append(result, grantType)
		}
	}
	if !slices.Contains(result, oauth2Module.HydraGrantTypeAuthorizationCode) && !slices.Contains(result, oauth2Module.HydraGrantTypeDeviceCode) {
		return nil, &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodeGrantTypeRequired, Message: "grantTypes requires authorization_code or the device code grant"}
	}
	return result, nil
}

// sanitizeAdminOAuthClientDevicePolicy keeps nil as nil (keep on update) and
// fills in the default maxCodesPer10m.
func sanitizeAdminOAuthClientDevicePolicy(payload *adminOAuthClientDevicePolicyPayload) (*adminOAuthClientDevicePolicyPayload, error) {
	if payload == nil {
		return nil, nil
	}
	maxCodes := oauth2Module.HydraDeviceMaxCodesPer10mDefault
	if payload.MaxCodesPer10m != nil {
		maxCodes = *payload.MaxCodesPer10m
	}
	if maxCodes < oauth2Module.HydraDeviceMaxCodesPer10mMin || maxCodes > oauth2Module.HydraDeviceMaxCodesPer10mMax {
		return nil, &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodeInvalidDevicePolicy, Message: "devicePolicy.maxCodesPer10m must be between 1 and 600"}
	}
	return &adminOAuthClientDevicePolicyPayload{FirstParty: payload.FirstParty, AllowWrite: payload.AllowWrite, MaxCodesPer10m: &maxCodes}, nil
}

// hydraDevicePolicy converts a sanitized payload policy; nil stays nil.
func (p *adminOAuthClientDevicePolicyPayload) hydraDevicePolicy() *oauth2Module.HydraOAuthClientDevicePolicy {
	if p == nil {
		return nil
	}
	policy := oauth2Module.DefaultHydraOAuthClientDevicePolicy()
	policy.FirstParty = p.FirstParty
	policy.AllowWrite = p.AllowWrite
	if p.MaxCodesPer10m != nil {
		policy.MaxCodesPer10m = *p.MaxCodesPer10m
	}
	return &policy
}

// validateAdminOAuthClientGrantRules applies the rules that depend on the
// effective grant types and device policy, i.e. the payload's when it carries
// them and otherwise the current client's (on create: the defaults). It runs in
// the handler after the current client is read: judged on the payload alone, a
// device-only client edited without grantTypes would look like an authorization
// code client missing its redirect URIs. It returns warnings for combinations
// that are allowed but never take effect.
func validateAdminOAuthClientGrantRules(payload *adminOAuthClientPayload, grantTypes []string, devicePolicy oauth2Module.HydraOAuthClientDevicePolicy) ([]string, error) {
	authorizationCode := slices.Contains(grantTypes, oauth2Module.HydraGrantTypeAuthorizationCode)
	device := slices.Contains(grantTypes, oauth2Module.HydraGrantTypeDeviceCode)
	if authorizationCode && len(payload.RedirectURIs) == 0 {
		return nil, &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodeRedirectURIsRequired, Message: "redirectUris is required"}
	}
	if slices.Contains(payload.Scopes, harukiOAuth2.ScopeOfflineAccess) && !slices.Contains(grantTypes, oauth2Module.HydraGrantTypeRefreshToken) {
		return nil, &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodeOfflineAccessRequiresRefresh, Message: "offline_access scope requires the refresh_token grant"}
	}
	// The device page echoes "authorized as <name>", which needs user:read.
	if device && !slices.Contains(payload.Scopes, harukiOAuth2.ScopeUserRead) {
		return nil, &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodeDeviceRequiresUserRead, Message: "the device code grant requires the user:read scope"}
	}
	// game-data:write over the device flow is for public clients (HarukiProxy)
	// only; a confidential client serves many users and stays read-only.
	if devicePolicy.AllowWrite && (payload.ClientType != "public" || !device || !slices.Contains(payload.Scopes, harukiOAuth2.ScopeGameDataWrite)) {
		return nil, &adminOAuthClientPayloadError{Code: adminOAuthClientErrorCodeDeviceWriteRequiresPublicClient, Message: "devicePolicy.allowWrite requires a public client with the device code grant and the game-data:write scope"}
	}
	var warnings []string
	if device && slices.Contains(payload.Scopes, harukiOAuth2.ScopeEmail) {
		warnings = append(warnings, "email scope is never granted over the device code grant")
	}
	return warnings, nil
}

// effectiveAdminOAuthClientGrantTypes is the payload's grant types when given,
// otherwise the current client's, or the create defaults without a client.
func effectiveAdminOAuthClientGrantTypes(payload *adminOAuthClientPayload, current *oauth2Module.HydraOAuthClient) []string {
	if payload.GrantTypes != nil {
		return payload.GrantTypes
	}
	if current != nil {
		return current.GrantTypes
	}
	return oauth2Module.DefaultHydraOAuthClientGrantTypes()
}

// effectiveAdminOAuthClientDevicePolicy is the payload's policy when given,
// otherwise the current client's stored one, or the default without a client.
func effectiveAdminOAuthClientDevicePolicy(payload *adminOAuthClientPayload, current *oauth2Module.HydraOAuthClient) oauth2Module.HydraOAuthClientDevicePolicy {
	if policy := payload.DevicePolicy.hydraDevicePolicy(); policy != nil {
		return *policy
	}
	return oauth2Module.HydraOAuthClientDevicePolicyOf(current)
}

// adminOAuthClientDeviceView returns the grantTypes, deviceEnabled and
// devicePolicy members of a client response.
func adminOAuthClientDeviceView(client *oauth2Module.HydraOAuthClient) ([]string, bool, adminOAuthClientDevicePolicy) {
	policy := oauth2Module.HydraOAuthClientDevicePolicyOf(client)
	return append([]string{}, client.GrantTypes...), oauth2Module.HydraOAuthClientDeviceEnabled(client), adminOAuthClientDevicePolicy{
		FirstParty:     policy.FirstParty,
		AllowWrite:     policy.AllowWrite,
		MaxCodesPer10m: policy.MaxCodesPer10m,
	}
}

// adminOAuthClientAuditDevicePolicy is the devicePolicy member of audit metadata.
func adminOAuthClientAuditDevicePolicy(policy adminOAuthClientDevicePolicy) map[string]any {
	return map[string]any{"firstParty": policy.FirstParty, "allowWrite": policy.AllowWrite, "maxCodesPer10m": policy.MaxCodesPer10m}
}

// adminOAuthClientPayloadFailureMetadata adds the error code of a coded payload
// error to the failure audit metadata.
func adminOAuthClientPayloadFailureMetadata(err error, extra map[string]any) map[string]any {
	var payloadErr *adminOAuthClientPayloadError
	if errors.As(err, &payloadErr) {
		if extra == nil {
			extra = map[string]any{}
		}
		extra["code"] = payloadErr.Code
	}
	return adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, extra)
}

// respondAdminOAuthClientPayloadError answers a coded payload error with its
// code in updatedData.code and any other error as before.
func respondAdminOAuthClientPayloadError(c fiber.Ctx, err error) error {
	var payloadErr *adminOAuthClientPayloadError
	if errors.As(err, &payloadErr) {
		return harukiAPIHelper.Responses.UpdatedDataResponse(c, fiber.StatusBadRequest, payloadErr.Message, &adminOAuthClientErrorData{Code: payloadErr.Code})
	}
	return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
}

// parseAdminOAuthClientPayload only sanitizes field by field. Rules that depend
// on the effective grant types are left to validateAdminOAuthClientGrantRules.
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
	postLogoutRedirectURIs, err := sanitizeAdminOAuthClientPostLogoutRedirectURIs(payload.PostLogoutRedirectURIs, redirectURIs)
	if err != nil {
		return nil, err
	}
	scopes, err := sanitizeAdminOAuthClientScopes(payload.Scopes)
	if err != nil {
		return nil, err
	}
	grantTypes, err := sanitizeAdminOAuthClientGrantTypes(payload.GrantTypes)
	if err != nil {
		return nil, err
	}
	devicePolicy, err := sanitizeAdminOAuthClientDevicePolicy(payload.DevicePolicy)
	if err != nil {
		return nil, err
	}
	payload.Name = name
	payload.ClientType = clientType
	payload.RedirectURIs = redirectURIs
	payload.PostLogoutRedirectURIs = postLogoutRedirectURIs
	payload.Scopes = scopes
	payload.GrantTypes = grantTypes
	payload.DevicePolicy = devicePolicy
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
	// Estimate capacity based on time range and bucket size
	var estimatedBuckets int
	bucketDuration := time.Hour // default hour
	switch bucket {
	case "day":
		bucketDuration = 24 * time.Hour
	case "hour":
		bucketDuration = time.Hour
	}
	if bucketDuration > 0 {
		estimatedBuckets = int(to.Sub(from)/bucketDuration) + 2
	} else {
		estimatedBuckets = 32
	}
	points := make([]adminOAuthClientTrendPoint, 0, estimatedBuckets)
	for cursor := truncateTimeByBucket(from.UTC(), bucket); !cursor.After(to.UTC()); cursor = nextTimeBucketStart(cursor, bucket) {
		bucketUnix := cursor.Unix()
		points = append(points, adminOAuthClientTrendPoint{BucketStart: cursor, AuthorizationCreated: authorizationCounts[bucketUnix], TokenIssued: tokenCounts[bucketUnix]})
	}
	return points
}
