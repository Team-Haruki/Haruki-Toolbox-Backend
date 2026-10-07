package oauth2

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	json "encoding/json/v2"
	"github.com/gofiber/fiber/v3"
)

const (
	deviceHydraTokenEndpointPath = "/oauth2/token"
	deviceAuditActionTokenIssued = "user.oauth.device.token_issued"
	deviceSlowDownText           = "polling too frequently"
	deviceAccessDeniedText       = "the user denied the authorization request"
	deviceExpiredTokenText       = "the device code has expired"
	deviceInvalidGrantText       = "the device code is invalid"
	deviceCodeRequiredText       = "device_code is required"
)

// deviceSettleRetryBackoff spaces the two retries of a settle that failed after
// Hydra already issued tokens.
var deviceSettleRetryBackoff = []time.Duration{50 * time.Millisecond, 150 * time.Millisecond}

// handleHydraTokenEndpoint is POST /api/oauth2/token. Every request that is not
// a form-encoded device_code grant is forwarded to Hydra byte for byte, exactly
// as handleHydraPublicProxy does for /revoke. The device branch unwraps the
// hdc_ code, applies slow_down locally and rewrites Hydra's answer by flow
// state (ory-suite-usage §10.5.4).
func handleHydraTokenEndpoint(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, cfg DeviceFlowConfig, store *deviceFlowStore) fiber.Handler {
	proxy := handleHydraPublicProxy(hydraConfig, deviceHydraTokenEndpointPath)
	shim := &deviceTokenShim{apiHelper: apiHelper, hydraConfig: hydraConfig, cfg: cfg, store: store}
	return func(c fiber.Ctx) error {
		form, ok := parseDeviceForm(c)
		if !ok || form.Get("grant_type") != HydraGrantTypeDeviceCode {
			return proxy(c)
		}
		return shim.handle(c, form)
	}
}

type deviceTokenShim struct {
	apiHelper   *harukiAPIHelper.HarukiToolboxRouterHelpers
	hydraConfig *harukiOAuth2.HydraConfig
	cfg         DeviceFlowConfig
	store       *deviceFlowStore
}

func (s *deviceTokenShim) logger() *harukiLogger.Logger { return s.cfg.log() }

func (s *deviceTokenShim) clientActive(ctx context.Context, clientID string) (string, error) {
	client, err := s.store.clients.get(ctx, func(ctx context.Context, id string) (*HydraOAuthClient, error) {
		return GetHydraOAuthClient(ctx, s.hydraConfig, id)
	}, clientID)
	if err != nil {
		return "", err
	}
	// A deleted client counts as disabled.
	if client != nil && HydraOAuthClientActive(client) {
		return "1", nil
	}
	return "0", nil
}

// Before Hydra has authenticated the client, the shim reveals only: malformed
// parameters (invalid_request), slow_down, an unknown code or a code of another
// client (invalid_grant), the feature being off (expired_token) and 503s.
// Denied, disabled and expired flows are only reported after Hydra accepted the
// client, so a poller with a wrong secret only ever sees Hydra's 401.
func (s *deviceTokenShim) handle(c fiber.Ctx, form url.Values) error {
	ctx := c.Context()
	setDeviceNoStore(c)

	for _, name := range []string{"grant_type", "device_code"} {
		if len(form[name]) > 1 {
			return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidRequest, deviceDuplicateParameterText)
		}
	}
	client, problem := resolveDeviceClient(c.Get(fiber.HeaderAuthorization), form)
	if problem != "" {
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidRequest, problem)
	}
	wrappedDeviceCode := form.Get("device_code")
	if wrappedDeviceCode == "" {
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidRequest, deviceCodeRequiredText)
	}
	raw, ok := parseWrappedDeviceCode(wrappedDeviceCode)
	if !ok {
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidGrant, deviceInvalidGrantText)
	}
	flowID, found, err := s.store.flowIDForDeviceCode(ctx, wrappedDeviceCode)
	if err != nil {
		return respondDeviceUnavailable(c)
	}
	if !found {
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidGrant, deviceInvalidGrantText)
	}

	active, err := s.cfg.Active(ctx)
	if err != nil {
		return respondDeviceUnavailable(c)
	}
	if !active {
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorExpiredToken, deviceExpiredTokenText)
	}

	poll, err := s.store.poll(ctx, flowID, client.ClientID, s.cfg.maxIntervalSeconds(), s.cfg.maxSlowDown())
	if err != nil {
		return respondDeviceUnavailable(c)
	}
	switch poll.Outcome {
	case devicePollMissing, devicePollClientMismatch:
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorInvalidGrant, deviceInvalidGrantText)
	case devicePollSlowDown:
		logDeviceEvent(s.logger(), "info", "poll_slow_down", flowID, client.ClientID, "interval="+strconv.Itoa(poll.IntervalSeconds))
		return respondDeviceOAuthBody(c, fiber.StatusBadRequest, deviceOAuthErrorBody{
			Error:            oauthErrorSlowDown,
			ErrorDescription: deviceSlowDownText,
			Interval:         poll.IntervalSeconds,
		})
	}

	// The client's enabled state is prefetched for approved flows, but only
	// used once Hydra has authenticated the client.
	clientActive := ""
	if isDeviceFlowApprovedClass(poll.State) {
		clientActive, err = s.clientActive(ctx, client.ClientID)
		if err != nil {
			return respondDeviceUnavailable(c)
		}
	}

	hydraDeviceCode, err := openHydraDeviceCode(raw, flowID, client.ClientID, poll.SealedDeviceCode)
	if err != nil {
		logDeviceEvent(s.logger(), "error", "poll", flowID, client.ClientID, "reason=unseal_failed")
		return respondDeviceOAuthError(c, fiber.StatusInternalServerError, oauthErrorServerError, deviceServerErrorText)
	}
	// H12: the flow's client ID (equal to the requesting client) and the
	// caller's Authorization, so Hydra authenticates the client itself.
	hydraBody := url.Values{
		"grant_type":  {HydraGrantTypeDeviceCode},
		"device_code": {hydraDeviceCode},
		"client_id":   {client.ClientID},
	}
	header := http.Header{}
	header.Set(fiber.HeaderContentType, formURLEncodedMediaType)
	header.Set(fiber.HeaderAccept, fiber.MIMEApplicationJSON)
	if authorization := strings.TrimSpace(c.Get(fiber.HeaderAuthorization)); authorization != "" {
		header.Set(fiber.HeaderAuthorization, authorization)
	}
	resp, _, err := forwardHydraPublicRequest(ctx, s.hydraConfig, http.MethodPost, deviceHydraTokenEndpointPath, "", []byte(hydraBody.Encode()), header)
	if err != nil {
		logDeviceEvent(s.logger(), "error", "poll", flowID, client.ClientID, "stage=H12", "reason=hydra_unavailable")
		return respondDeviceUnavailable(c)
	}

	result := classifyHydraDeviceTokenResponse(resp)
	if result == deviceHydraResultOther {
		// Includes Hydra's 401 for a wrong secret: relayed without any
		// flow state.
		return respondDeviceHydraPassthrough(c, resp)
	}

	settled, err := s.settle(ctx, flowID, wrappedDeviceCode, result, clientActive)
	if err == nil && settled.Action == deviceSettleCheckClient {
		// The flow became approved after the poll script ran.
		clientActive, err = s.clientActive(ctx, client.ClientID)
		if err == nil {
			settled, err = s.settle(ctx, flowID, wrappedDeviceCode, result, clientActive)
		}
	}
	if err != nil || settled.Action == deviceSettleCheckClient {
		return s.respondSettleFailure(c, flowID, client.ClientID, result, poll, clientActive, resp)
	}

	switch settled.Action {
	case deviceSettleToken:
		s.recordTokenIssued(c, flowID, client.ClientID, settled.ClaimedBy)
		return respondDeviceHydraPassthrough(c, resp)
	case deviceSettleAccessDenied:
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorAccessDenied, deviceAccessDeniedText)
	case deviceSettleExpiredToken:
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorExpiredToken, deviceExpiredTokenText)
	case deviceSettleRevokeDenied:
		// The client was disabled after approval: the tokens Hydra just issued
		// are discarded and their consent session revoked (cascading to them).
		s.revokeWithheldTokens(ctx, flowID, client.ClientID, settled.ConsentRequestID)
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorAccessDenied, deviceAccessDeniedText)
	case deviceSettleRevokeTerminalDenied, deviceSettleRevokeTerminalExpired:
		logDeviceEvent(s.logger(), "warn", "token_after_terminal", flowID, client.ClientID, "status="+settled.State)
		s.revokeWithheldTokens(ctx, flowID, client.ClientID, settled.ConsentRequestID)
		if settled.Action == deviceSettleRevokeTerminalDenied {
			return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorAccessDenied, deviceAccessDeniedText)
		}
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorExpiredToken, deviceExpiredTokenText)
	case deviceSettleGone:
		if result == deviceHydraResultOK {
			logDeviceEvent(s.logger(), "warn", "token_after_terminal", flowID, client.ClientID, "status=gone")
			s.revokeWithheldTokens(ctx, flowID, client.ClientID, poll.ConsentRequestID)
			return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorExpiredToken, deviceExpiredTokenText)
		}
	}
	return respondDeviceHydraPassthrough(c, resp)
}

// settle runs the settle script; after Hydra issued tokens a failure is
// retried twice with a short backoff before giving up.
func (s *deviceTokenShim) settle(ctx context.Context, flowID, wrappedDeviceCode, result, clientActive string) (deviceSettleResult, error) {
	settled, err := s.store.settle(ctx, flowID, wrappedDeviceCode, result, clientActive)
	if err == nil || result != deviceHydraResultOK {
		return settled, err
	}
	for _, backoff := range deviceSettleRetryBackoff {
		select {
		case <-ctx.Done():
			return settled, err
		case <-time.After(backoff):
		}
		if settled, err = s.store.settle(ctx, flowID, wrappedDeviceCode, result, clientActive); err == nil {
			return settled, nil
		}
	}
	return settled, err
}

// respondSettleFailure answers when the flow could not be settled. Tokens Hydra
// already issued for an approved flow of an enabled client are still handed
// out (settle_failed): the flow stays in the unredeemed set, so the reaper
// revokes them at exp + grace and the device has to bind again. Otherwise no
// token is handed out.
func (s *deviceTokenShim) respondSettleFailure(c fiber.Ctx, flowID, clientID, result string, poll devicePollResult, clientActive string, resp *hydraPublicResponse) error {
	if result != deviceHydraResultOK {
		return respondDeviceUnavailable(c)
	}
	logDeviceEvent(s.logger(), "error", "settle_failed", flowID, clientID, "status="+poll.State)
	if isDeviceFlowApprovedClass(poll.State) && clientActive == "1" {
		return respondDeviceHydraPassthrough(c, resp)
	}
	s.revokeWithheldTokens(c.Context(), flowID, clientID, poll.ConsentRequestID)
	if poll.State == deviceFlowStateDenied || clientActive == "0" {
		return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorAccessDenied, deviceAccessDeniedText)
	}
	return respondDeviceOAuthError(c, fiber.StatusBadRequest, oauthErrorExpiredToken, deviceExpiredTokenText)
}

// revokeWithheldTokens revokes the consent session of tokens Hydra issued but
// the shim did not hand out, and drops the flow from the unredeemed set once
// that succeeded; on failure the reaper retries.
func (s *deviceTokenShim) revokeWithheldTokens(ctx context.Context, flowID, clientID, consentRequestID string) {
	if consentRequestID == "" {
		// Cannot happen by design: a flow is approved only after its consent
		// request ID was recorded.
		logDeviceEvent(s.logger(), "error", "token_after_terminal", flowID, clientID, "reason=missing_consent_request_id")
		return
	}
	if err := RevokeHydraConsentSessionByID(ctx, s.hydraConfig, consentRequestID); err != nil {
		logDeviceEvent(s.logger(), "error", "token_after_terminal", flowID, clientID, "reason=revoke_failed")
		return
	}
	if err := s.store.removeUnredeemed(ctx, flowID); err != nil {
		logDeviceEvent(s.logger(), "warn", "token_after_terminal", flowID, clientID, "reason=unredeemed_remove_failed")
	}
}

// recordTokenIssued audits a hand-out. The request is anonymous, so the audit
// is a system log with an anonymous actor and the claiming user as target;
// WriteUserAuditLog would record the target as its own actor.
func (s *deviceTokenShim) recordTokenIssued(c fiber.Ctx, flowID, clientID, claimedBy string) {
	logDeviceEvent(s.logger(), "info", "token_issued", flowID, clientID)
	targetType := "user"
	entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, deviceAuditActionTokenIssued, harukiAPIHelper.SystemLogResultSuccess, &targetType, &claimedBy, map[string]any{
		"clientID":     clientID,
		"deviceFlowID": flowID,
	})
	if err := harukiAPIHelper.WriteSystemLog(c.Context(), s.apiHelper, entry); err != nil {
		logDeviceEvent(s.logger(), "warn", "token_issued", flowID, clientID, "reason=audit_failed")
	}
}

// classifyHydraDeviceTokenResponse maps Hydra's token answer to a settle
// result: 200, and the 400s authorization_pending, expired_token and
// invalid_grant; everything else (401 included) is "other".
func classifyHydraDeviceTokenResponse(resp *hydraPublicResponse) string {
	if resp.Status == http.StatusOK {
		return deviceHydraResultOK
	}
	if resp.Status != http.StatusBadRequest {
		return deviceHydraResultOther
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return deviceHydraResultOther
	}
	switch body.Error {
	case oauthErrorAuthorizationPending:
		return deviceHydraResultPending
	case oauthErrorExpiredToken:
		return deviceHydraResultExpiredToken
	case oauthErrorInvalidGrant:
		return deviceHydraResultInvalidGrant
	}
	return deviceHydraResultOther
}
