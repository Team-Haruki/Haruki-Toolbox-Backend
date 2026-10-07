package oauth2

import (
	"context"
	"encoding/base64"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	json "encoding/json/v2"
	"github.com/gofiber/fiber/v3"
)

const (
	deviceAuthorizationMaxBodyBytes = 4096
	formURLEncodedMediaType         = "application/x-www-form-urlencoded"

	// RFC 6749 / RFC 8628 error codes the backend itself produces.
	oauthErrorInvalidRequest          = "invalid_request"
	oauthErrorInvalidClient           = "invalid_client"
	oauthErrorUnauthorizedClient      = "unauthorized_client"
	oauthErrorInvalidScope            = "invalid_scope"
	oauthErrorInvalidGrant            = "invalid_grant"
	oauthErrorSlowDown                = "slow_down"
	oauthErrorAccessDenied            = "access_denied"
	oauthErrorExpiredToken            = "expired_token"
	oauthErrorTemporarilyUnavailable  = "temporarily_unavailable"
	oauthErrorServerError             = "server_error"
	oauthErrorAuthorizationPending    = "authorization_pending"
	deviceTemporarilyUnavailableText  = "the service is temporarily unavailable, retry later"
	deviceFeatureUnavailableText      = "device authorization is not available for this client"
	deviceRateLimitedText             = "too many device authorization requests, retry later"
	deviceServerErrorText             = "the device authorization could not be completed"
	deviceInvalidClientText           = "client authentication failed"
	deviceMalformedClientAuthText     = "malformed client authentication"
	deviceClientIDRequiredText        = "client_id is required"
	deviceClientIDMismatchText        = "client_id does not match the authenticated client"
	deviceDuplicateParameterText      = "request parameters must not be repeated"
	deviceAudienceUnsupportedText     = "audience is not supported for device authorization"
	deviceUnsupportedContentTypeText  = "the request body must be application/x-www-form-urlencoded"
	deviceRequestTooLargeText         = "the request body is too large"
	deviceMalformedBodyText           = "the request body could not be parsed"
	deviceHydraDeviceAuthEndpointPath = "/oauth2/device/auth"
)

// deviceOAuthErrorBody is the RFC 6749 error body of device-facing endpoints.
// Interval is only set on slow_down.
type deviceOAuthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
	Interval         int    `json:"interval,omitzero"`
}

// deviceAuthorizationResponse is the RFC 8628 §3.2 response. device_code is
// the wrapped hdc_ code and verification_uri the frontend's short address.
type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// hydraDeviceAuthorizationResponse is Hydra's device/auth answer. Hydra also
// serializes a stray "Header" member, which is ignored.
type hydraDeviceAuthorizationResponse struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
}

// setDeviceNoStore marks a device-facing response as uncacheable; every
// response of device/auth and of the token endpoint's device branch has it.
func setDeviceNoStore(c fiber.Ctx) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderPragma, "no-cache")
}

func respondDeviceOAuthError(c fiber.Ctx, status int, code, description string) error {
	return respondDeviceOAuthBody(c, status, deviceOAuthErrorBody{Error: code, ErrorDescription: description})
}

func respondDeviceOAuthBody(c fiber.Ctx, status int, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	setDeviceNoStore(c)
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(status).Send(encoded)
}

func respondDeviceUnavailable(c fiber.Ctx) error {
	return respondDeviceOAuthError(c, fiber.StatusServiceUnavailable, oauthErrorTemporarilyUnavailable, deviceTemporarilyUnavailableText)
}

// respondDeviceHydraPassthrough relays a Hydra response unchanged except for
// the no-store headers.
func respondDeviceHydraPassthrough(c fiber.Ctx, resp *hydraPublicResponse) error {
	copyHydraResponseHeaders(c, resp.Header)
	setDeviceNoStore(c)
	c.Status(resp.Status)
	if len(resp.Body) == 0 {
		return nil
	}
	return c.Send(resp.Body)
}

// logDeviceEvent writes one oauth2_device structured line; fields are
// "key=value" strings. Only the flow ID, client ID and enumerated values may be
// passed: never a user code, wrapped or Hydra device code, flow handle,
// challenge, verifier or URL.
func logDeviceEvent(logger *harukiLogger.Logger, level string, event, flowID, clientID string, fields ...string) {
	var b strings.Builder
	b.WriteString("oauth2_device event=")
	b.WriteString(event)
	if flowID != "" {
		b.WriteString(" fid=")
		b.WriteString(flowID)
	}
	if clientID != "" {
		b.WriteString(" cid=")
		b.WriteString(strconv.Quote(clientID))
	}
	for _, field := range fields {
		b.WriteString(" ")
		b.WriteString(field)
	}
	logger = deviceFlowLogger(logger)
	switch level {
	case "error":
		logger.Errorf("%s", b.String())
	case "warn":
		logger.Warnf("%s", b.String())
	default:
		logger.Infof("%s", b.String())
	}
}

// deviceClientAuth is the client a device-facing request names: the Basic
// username when Basic is used (RFC 6749 §2.3.1: both parts are form-encoded),
// otherwise the form client_id.
type deviceClientAuth struct {
	ClientID string
	Basic    bool
}

// resolveDeviceClient applies the client rules shared by device/auth and the
// token endpoint's device branch. It returns an error description for
// invalid_request.
func resolveDeviceClient(authorization string, form url.Values) (deviceClientAuth, string) {
	formClientIDs := form["client_id"]
	if len(formClientIDs) > 1 {
		return deviceClientAuth{}, deviceDuplicateParameterText
	}
	formClientID := ""
	if len(formClientIDs) == 1 {
		formClientID = formClientIDs[0]
	}
	authorization = strings.TrimSpace(authorization)
	if authorization == "" {
		if formClientID == "" {
			return deviceClientAuth{}, deviceClientIDRequiredText
		}
		return deviceClientAuth{ClientID: formClientID}, ""
	}
	scheme, credentials, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return deviceClientAuth{}, deviceMalformedClientAuthText
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(credentials))
	if err != nil {
		return deviceClientAuth{}, deviceMalformedClientAuthText
	}
	rawUser, rawPassword, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return deviceClientAuth{}, deviceMalformedClientAuthText
	}
	user, userErr := url.QueryUnescape(rawUser)
	_, passwordErr := url.QueryUnescape(rawPassword)
	if userErr != nil || passwordErr != nil || user == "" {
		return deviceClientAuth{}, deviceMalformedClientAuthText
	}
	if formClientID != "" && formClientID != user {
		return deviceClientAuth{}, deviceClientIDMismatchText
	}
	return deviceClientAuth{ClientID: user, Basic: true}, ""
}

// parseDeviceForm reads an application/x-www-form-urlencoded body.
func parseDeviceForm(c fiber.Ctx) (url.Values, bool) {
	mediaType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	if err != nil || mediaType != formURLEncodedMediaType {
		return nil, false
	}
	form, err := url.ParseQuery(string(c.Body()))
	if err != nil {
		return nil, false
	}
	return form, true
}

type deviceAuthorizationRequest struct {
	client      deviceClientAuth
	scopes      []string
	deviceLabel string
}

// parseDeviceAuthorizationRequest validates the request without touching Redis
// or Hydra (ory-suite-usage §10.5.3 step 1). It returns an invalid_request description.
func parseDeviceAuthorizationRequest(c fiber.Ctx) (deviceAuthorizationRequest, string) {
	mediaType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	if err != nil || mediaType != formURLEncodedMediaType {
		return deviceAuthorizationRequest{}, deviceUnsupportedContentTypeText
	}
	if len(c.Body()) > deviceAuthorizationMaxBodyBytes {
		return deviceAuthorizationRequest{}, deviceRequestTooLargeText
	}
	form, err := url.ParseQuery(string(c.Body()))
	if err != nil {
		return deviceAuthorizationRequest{}, deviceMalformedBodyText
	}
	for _, name := range []string{"scope", "device_label"} {
		if len(form[name]) > 1 {
			return deviceAuthorizationRequest{}, deviceDuplicateParameterText
		}
	}
	if _, ok := form["audience"]; ok {
		return deviceAuthorizationRequest{}, deviceAudienceUnsupportedText
	}
	client, problem := resolveDeviceClient(c.Get(fiber.HeaderAuthorization), form)
	if problem != "" {
		return deviceAuthorizationRequest{}, problem
	}
	// Any other member, haruki_* included, is dropped and never forwarded.
	return deviceAuthorizationRequest{
		client:      client,
		scopes:      normalizeDeviceScope(form.Get("scope")),
		deviceLabel: sanitizeDeviceLabel(form.Get("device_label")),
	}, ""
}

// handleHydraDeviceAuthorization proxies RFC 8628 device authorization
// (POST /api/oauth2/device/auth) to Hydra, which cannot rate-limit, ignores
// metadata.haruki.active and points verification_uri at the API host. The
// order of checks is fixed by ory-suite-usage §10.5.3: the client lookup comes first, and
// only the issuance reservation can refuse, so a flood naming a confidential
// client with a wrong secret cannot lock the real client out.
func handleHydraDeviceAuthorization(hydraConfig *harukiOAuth2.HydraConfig, cfg DeviceFlowConfig, store *deviceFlowStore) fiber.Handler {
	lookupClient := func(ctx context.Context, clientID string) (*HydraOAuthClient, error) {
		return GetHydraOAuthClient(ctx, hydraConfig, clientID)
	}
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		logger := cfg.log()
		request, problem := parseDeviceAuthorizationRequest(c)
		if problem != "" {
			return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidRequest, problem)
		}

		active, err := cfg.Active(ctx)
		if err != nil {
			logDeviceEvent(logger, "error", "authorize", "", "", "stage=gate", "reason=runtime_config_unavailable")
			return respondDeviceUnavailable(c)
		}
		if !active {
			return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorUnauthorizedClient, deviceFeatureUnavailableText)
		}

		client, err := store.clients.get(ctx, lookupClient, request.client.ClientID)
		if err != nil {
			logDeviceEvent(logger, "error", "authorize", "", "", "stage=client_lookup", "reason=hydra_unavailable")
			return respondDeviceUnavailable(c)
		}
		limits := cfg.Limits()
		manager, err := store.redisManager()
		if err != nil {
			return respondDeviceUnavailable(c)
		}
		keys := manager.KeyBuilder()
		if client == nil {
			// Warn-only; the client ID is attacker-chosen, so it is not logged.
			count, countErr := store.increment(ctx, keys.BuildOAuth2DeviceAuthAttemptUnknownClientKey(), deviceRateWindow)
			if countErr == nil && count == int64(limits.AuthAttemptUnknownClientWarnPer10m) {
				logDeviceEvent(logger, "warn", "auth_unknown_client_warn", "", "", "count="+strconv.FormatInt(count, 10))
			}
			if request.client.Basic {
				c.Set(fiber.HeaderWWWAuthenticate, `Basic realm="oauth2"`)
			}
			return respondDeviceOAuthError(c, fiber.StatusUnauthorized, oauthErrorInvalidClient, deviceInvalidClientText)
		}
		clientID := client.ClientID
		// A client without a stored quota gets auth_issued_client_default_per_10m.
		maxCodes, ok := hydraOAuthClientStoredMaxCodesPer10m(client)
		if !ok {
			maxCodes = limits.AuthIssuedClientDefaultPer10m
		}

		// Warn-only as well: a confidential client's secret is only checked by
		// Hydra at H3, so refusing here would let a flood with a wrong secret
		// lock the real client out.
		attempts, err := store.increment(ctx, keys.BuildOAuth2DeviceAuthAttemptClientKey(clientID), deviceRateWindow)
		if err != nil {
			return respondDeviceUnavailable(c)
		}
		if attempts == int64(limits.AuthAttemptClientWarnMultiplier*maxCodes) {
			logDeviceEvent(logger, "warn", "auth_attempt_client_warn", "", clientID, "count="+strconv.FormatInt(attempts, 10))
		}

		if !deviceClientUsable(cfg, client) {
			return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorUnauthorizedClient, deviceFeatureUnavailableText)
		}
		if description, ok := checkDeviceScopePolicy(client, request.scopes); !ok {
			return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidScope, description)
		}

		clientType := HydraClientTypeFromAuthMethod(client.TokenEndpointAuthMethod)
		poolLimit := limits.AuthIssuedPublicPer10m
		if clientType == oauthClientTypeConfidential {
			poolLimit = limits.AuthIssuedConfidentialPer10m
		}
		counters := []deviceRateCounter{
			{Key: keys.BuildOAuth2DeviceAuthIssuedPoolKey(clientType), Limit: poolLimit, Window: deviceRateWindow},
			{Key: keys.BuildOAuth2DeviceAuthIssuedClientKey(clientID), Limit: maxCodes, Window: deviceRateWindow},
		}
		reservation, err := store.reserve(ctx, counters)
		if err != nil {
			return respondDeviceUnavailable(c)
		}
		if reservation.LimitedIndex >= 0 {
			limited := counters[reservation.LimitedIndex]
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(store.retryAfterSeconds(ctx, limited.Key, limited.Window)))
			return respondDeviceOAuthError(c, fiber.StatusTooManyRequests, oauthErrorTemporarilyUnavailable, deviceRateLimitedText)
		}
		if reservation.Counts[0] == deviceWarnThreshold(poolLimit) {
			logDeviceEvent(logger, "warn", "auth_issued_global_warn", "", "", "pool="+clientType)
		}

		// H3: only client_id and scope reach Hydra, with the caller's
		// Authorization so Hydra authenticates the client.
		hydraBody := url.Values{"client_id": {clientID}, "scope": {strings.Join(request.scopes, " ")}}
		header := http.Header{}
		header.Set(fiber.HeaderContentType, formURLEncodedMediaType)
		header.Set(fiber.HeaderAccept, fiber.MIMEApplicationJSON)
		if authorization := strings.TrimSpace(c.Get(fiber.HeaderAuthorization)); authorization != "" {
			header.Set(fiber.HeaderAuthorization, authorization)
		}
		resp, _, err := forwardHydraPublicRequest(ctx, hydraConfig, http.MethodPost, deviceHydraDeviceAuthEndpointPath, "", []byte(hydraBody.Encode()), header)
		if err != nil {
			releaseDeviceReservation(ctx, store, counters, logger, clientID)
			logDeviceEvent(logger, "error", "authorize", "", clientID, "stage=H3", "reason=hydra_unavailable")
			return respondDeviceUnavailable(c)
		}
		if resp.Status != http.StatusOK {
			releaseDeviceReservation(ctx, store, counters, logger, clientID)
			logDeviceEvent(logger, "info", "authorize", "", clientID, "stage=H3", "status="+strconv.Itoa(resp.Status), "hydra_error="+hydraOAuthErrorCode(resp.Body))
			return respondDeviceHydraPassthrough(c, resp)
		}

		var hydraResp hydraDeviceAuthorizationResponse
		if err := json.Unmarshal(resp.Body, &hydraResp); err != nil || hydraResp.DeviceCode == "" {
			logDeviceEvent(logger, "error", "authorize", "", clientID, "stage=H3", "reason=malformed_hydra_response")
			return respondDeviceOAuthError(c, fiber.StatusInternalServerError, oauthErrorServerError, deviceServerErrorText)
		}
		// Self-checks fail closed when Hydra and the backend drift apart.
		normalizedUserCode, ok := normalizeDeviceUserCode(hydraResp.UserCode, cfg.UserCodeCharset(), cfg.UserCodeLength())
		if !ok || normalizedUserCode != hydraResp.UserCode {
			logDeviceEvent(logger, "error", "charset_mismatch", "", clientID)
			return respondDeviceOAuthError(c, fiber.StatusInternalServerError, oauthErrorServerError, deviceServerErrorText)
		}
		expiresIn := time.Duration(hydraResp.ExpiresIn) * time.Second
		if hydraResp.ExpiresIn <= 0 || expiresIn > cfg.UserCodeTTL()+5*time.Second {
			logDeviceEvent(logger, "error", "ttl_mismatch", "", clientID, "expires_in="+strconv.Itoa(hydraResp.ExpiresIn))
			return respondDeviceOAuthError(c, fiber.StatusInternalServerError, oauthErrorServerError, deviceServerErrorText)
		}

		flowID := newDeviceFlowID()
		wrappedDeviceCode, raw := newWrappedDeviceCode()
		sealed, err := sealHydraDeviceCode(raw, flowID, clientID, hydraResp.DeviceCode)
		if err != nil {
			logDeviceEvent(logger, "error", "authorize", flowID, clientID, "reason=seal_failed")
			return respondDeviceOAuthError(c, fiber.StatusInternalServerError, oauthErrorServerError, deviceServerErrorText)
		}
		interval := max(hydraResp.Interval, int(cfg.minPollInterval()/time.Second))
		created, err := store.create(ctx, deviceFlowRecord{
			FlowID:             flowID,
			WrappedDeviceCode:  wrappedDeviceCode,
			NormalizedUserCode: normalizedUserCode,
			ClientID:           clientID,
			ClientType:         clientType,
			Scope:              strings.Join(request.scopes, " "),
			DeviceLabel:        request.deviceLabel,
			SealedDeviceCode:   sealed,
			CreatedAt:          store.now(),
			ExpiresIn:          expiresIn,
			IntervalSeconds:    interval,
		}, cfg.recordGrace())
		if err != nil {
			// Hydra's row is left for the janitor.
			logDeviceEvent(logger, "error", "authorize", flowID, clientID, "reason=store_unavailable")
			return respondDeviceUnavailable(c)
		}
		if !created {
			logDeviceEvent(logger, "error", "authorize", flowID, clientID, "reason=uc_collision")
			return respondDeviceOAuthError(c, fiber.StatusInternalServerError, oauthErrorServerError, deviceServerErrorText)
		}
		logDeviceEvent(logger, "info", "authorize", flowID, clientID, "ctype="+clientType)

		formattedUserCode := formatDeviceUserCode(normalizedUserCode)
		return respondDeviceOAuthBody(c, fiber.StatusOK, deviceAuthorizationResponse{
			DeviceCode:              wrappedDeviceCode,
			UserCode:                formattedUserCode,
			VerificationURI:         cfg.VerificationURL(),
			VerificationURIComplete: cfg.VerificationURLComplete(formattedUserCode),
			ExpiresIn:               hydraResp.ExpiresIn,
			Interval:                interval,
		})
	}
}

func releaseDeviceReservation(ctx context.Context, store *deviceFlowStore, counters []deviceRateCounter, logger *harukiLogger.Logger, clientID string) {
	if err := store.release(ctx, counters); err != nil {
		logDeviceEvent(logger, "warn", "authorize", "", clientID, "reason=release_failed")
	}
}

// hydraOAuthErrorCode extracts the RFC 6749 error code of a Hydra error body
// for logging; anything else is reported as "unknown".
func hydraOAuthErrorCode(body []byte) string {
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error == "" {
		return "unknown"
	}
	for _, r := range parsed.Error {
		if (r < 'a' || r > 'z') && r != '_' {
			return "unknown"
		}
	}
	return parsed.Error
}
