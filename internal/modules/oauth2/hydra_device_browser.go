package oauth2

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"mime"
	"strconv"
	"strings"
	"time"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	json "encoding/json/v2"
	"github.com/gofiber/fiber/v3"
)

// The browser endpoints of the device flow (design §6.4): lookup claims a
// user code for the signed-in user and returns the review card; approve runs
// the server-driven chain (hydra_device_verification.go); deny records the
// refusal. They sit behind Oathkeeper's cookie_session rule and the session
// guard, never answer 401 themselves (the frontend treats 401 as an expired
// session), never relay Hydra's text, and answer the fixed codes of §6.5 in
// updatedData.code.

const (
	deviceBrowserMaxBodyBytes = 1024
	deviceFlowHandlePrefix    = "dfh_"
	deviceFlowNonceBytes      = 16
	jsonMediaType             = "application/json"
	deviceAuditActionClaim    = "user.oauth.device.claim"
	deviceAuditActionApprove  = "user.oauth.device.approve"
	deviceAuditActionDeny     = "user.oauth.device.deny"
	deviceDenyReasonUser      = "user_denied"
	deviceDenyReasonNotMine   = "not_initiated_by_me"
	deviceLabelSourceUser     = "user"
	deviceLabelSourceDevice   = "device"
	deviceLabelSourceDefault  = "default"
	// deviceLookupGlobalWarnPart: lookup-fail:global warns at a third of its
	// hard cap (50 of 150, design §8.2).
	deviceLookupGlobalWarnPart = 3
)

// Browser error codes (design §6.5; the table is fixed).
const (
	deviceCodeFeatureDisabled        = "feature_disabled"
	deviceCodeUnsupportedMediaType   = "unsupported_media_type"
	deviceCodeOriginRejected         = "origin_rejected"
	deviceCodeInvalidRequest         = "invalid_request"
	deviceCodeMalformedCode          = "malformed_code"
	deviceCodeInvalidCode            = "invalid_code"
	deviceCodeRateLimited            = "rate_limited"
	deviceCodeCodeExpired            = "code_expired"
	deviceCodeAlreadyHandled         = "already_handled"
	deviceCodeFlowConflict           = "flow_conflict"
	deviceCodeSessionChanged         = "session_changed"
	deviceCodeAckRequired            = "ack_required"
	deviceCodeClientUnavailable      = "client_unavailable"
	deviceCodeApprovalFailed         = "approval_failed"
	deviceCodeTemporarilyUnavailable = "temporarily_unavailable"
)

// deviceBrowserMessages are the fixed English messages of the codes. Pages
// branch on the code only and never show the message.
var deviceBrowserMessages = map[string]string{
	deviceCodeFeatureDisabled:        "device authorization is not available",
	deviceCodeUnsupportedMediaType:   "the request body must be application/json",
	deviceCodeOriginRejected:         "the request origin is not allowed",
	deviceCodeInvalidRequest:         "the request is invalid",
	deviceCodeMalformedCode:          "the code is not well formed",
	deviceCodeInvalidCode:            "the code is invalid, expired or already used by another account; get a new code on the device",
	deviceCodeRateLimited:            "too many requests, retry later",
	deviceCodeCodeExpired:            "the code has expired; get a new code on the device",
	deviceCodeAlreadyHandled:         "this request has already been handled",
	deviceCodeFlowConflict:           "this request is no longer current; enter the code again",
	deviceCodeSessionChanged:         "this request was opened in another session; enter the code again",
	deviceCodeAckRequired:            "confirm that you started this request yourself",
	deviceCodeClientUnavailable:      "this application cannot be authorized from a device",
	deviceCodeApprovalFailed:         "the authorization could not be completed",
	deviceCodeTemporarilyUnavailable: "the service is temporarily unavailable, retry later",
}

// deviceBrowserErrorData is updatedData of a browser error.
type deviceBrowserErrorData struct {
	Code       string `json:"code"`
	RetryAfter int    `json:"retryAfter,omitzero"`
	Retryable  *bool  `json:"retryable,omitzero"`
}

func respondDeviceBrowserError(c fiber.Ctx, status int, code string) error {
	return respondDeviceBrowserErrorData(c, status, deviceBrowserErrorData{Code: code})
}

func respondDeviceBrowserErrorData(c fiber.Ctx, status int, data deviceBrowserErrorData) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, status, deviceBrowserMessages[data.Code], &data)
}

func respondDeviceBrowserRateLimited(c fiber.Ctx, retryAfter int) error {
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retryAfter))
	return respondDeviceBrowserErrorData(c, fiber.StatusTooManyRequests, deviceBrowserErrorData{Code: deviceCodeRateLimited, RetryAfter: retryAfter})
}

func respondDeviceApprovalFailed(c fiber.Ctx, retryable bool) error {
	return respondDeviceBrowserErrorData(c, fiber.StatusBadGateway, deviceBrowserErrorData{Code: deviceCodeApprovalFailed, Retryable: &retryable})
}

func respondDeviceBrowserUnavailable(c fiber.Ctx) error {
	return respondDeviceBrowserError(c, fiber.StatusServiceUnavailable, deviceCodeTemporarilyUnavailable)
}

func respondDeviceBrowserSuccess[T any](c fiber.Ctx, status int, message string, data *T) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, status, message, data)
}

// deviceScopeRisks classifies the scopes shown on the review card. A scope
// missing from the table is shown as write, the most cautious class; a new
// grantable scope registers its class here (e.g. station:room:write → write).
var deviceScopeRisks = map[string]string{
	harukiOAuth2.ScopeOpenID:           deviceScopeRiskIdentity,
	harukiOAuth2.ScopeProfile:          deviceScopeRiskIdentity,
	harukiOAuth2.ScopeOfflineAccess:    deviceScopeRiskOffline,
	harukiOAuth2.ScopeUserRead:         deviceScopeRiskRead,
	harukiOAuth2.ScopeBindingsRead:     deviceScopeRiskRead,
	harukiOAuth2.ScopeGameDataRead:     deviceScopeRiskRead,
	harukiOAuth2.ScopeGameDataWrite:    deviceScopeRiskWrite,
	harukiOAuth2.ScopeStationRoomWrite: deviceScopeRiskWrite,
}

const (
	deviceScopeRiskIdentity = "identity"
	deviceScopeRiskRead     = "read"
	deviceScopeRiskWrite    = "write"
	deviceScopeRiskOffline  = "offline"
)

func deviceScopeRisk(scope string) string {
	if risk, ok := deviceScopeRisks[scope]; ok {
		return risk
	}
	return deviceScopeRiskWrite
}

// Review card payload of a successful lookup.
type deviceReviewClient struct {
	ClientID          string `json:"clientId"`
	ClientName        string `json:"clientName"`
	ClientType        string `json:"clientType"`
	FirstParty        bool   `json:"firstParty"`
	InitiatorVerified bool   `json:"initiatorVerified"`
}

type deviceReviewScope struct {
	Scope string `json:"scope"`
	Risk  string `json:"risk"`
}

type deviceReviewAccount struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
}

type deviceReviewCard struct {
	FlowHandle   string              `json:"flowHandle"`
	UserCode     string              `json:"userCode"`
	Client       deviceReviewClient  `json:"client"`
	Scopes       []deviceReviewScope `json:"scopes"`
	DeviceLabel  string              `json:"deviceLabel"`
	RequestedAt  string              `json:"requestedAt"`
	ExpiresAt    string              `json:"expiresAt"`
	Account      deviceReviewAccount `json:"account"`
	WriteWarning bool                `json:"writeWarning"`
}

type deviceApproveResponse struct {
	Status           string `json:"status"`
	ClientName       string `json:"clientName,omitzero"`
	ConsentRequestID string `json:"consentRequestId,omitzero"`
	AccountName      string `json:"accountName,omitzero"`
}

type deviceDenyResponse struct {
	Status string `json:"status"`
}

type deviceLookupRequest struct {
	UserCode string `json:"userCode"`
}

type deviceApproveRequest struct {
	FlowHandle   string `json:"flowHandle"`
	UserCode     string `json:"userCode"`
	Label        string `json:"label"`
	Acknowledged bool   `json:"acknowledged"`
}

type deviceDenyRequest struct {
	FlowHandle string `json:"flowHandle"`
	Reason     string `json:"reason"`
}

// deviceBrowserHandlers holds what lookup, approve and deny share.
type deviceBrowserHandlers struct {
	apiHelper   *harukiAPIHelper.HarukiToolboxRouterHelpers
	hydraConfig *harukiOAuth2.HydraConfig
	cfg         DeviceFlowConfig
	store       *deviceFlowStore
}

func newDeviceBrowserHandlers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, cfg DeviceFlowConfig, store *deviceFlowStore) *deviceBrowserHandlers {
	return &deviceBrowserHandlers{apiHelper: apiHelper, hydraConfig: hydraConfig, cfg: cfg, store: store}
}

func (h *deviceBrowserHandlers) logger() *harukiLogger.Logger { return h.cfg.log() }

// deviceBrowserUser is the signed-in user deciding a flow.
type deviceBrowserUser struct {
	ID          string
	SessionHash string
}

// guard applies the checks every browser endpoint starts with, in the order
// of design §6.4: feature switch, JSON content type, Origin allowlist, body
// size. It returns handled=true when it already answered.
func (h *deviceBrowserHandlers) guard(c fiber.Ctx) (bool, error) {
	active, err := h.cfg.Active(c.Context())
	if err != nil {
		logDeviceEvent(h.logger(), "error", "lookup_fail", "", "", "stage=gate", "reason=runtime_config_unavailable")
		return true, respondDeviceBrowserUnavailable(c)
	}
	if !active {
		return true, respondDeviceBrowserError(c, fiber.StatusForbidden, deviceCodeFeatureDisabled)
	}
	// Only JSON: Fiber also binds forms, and the Kratos cookie covers every
	// subdomain, so a cross-site form post must not get this far.
	if mediaType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType)); err != nil || mediaType != jsonMediaType {
		return true, respondDeviceBrowserError(c, fiber.StatusUnsupportedMediaType, deviceCodeUnsupportedMediaType)
	}
	if !h.cfg.OriginAllowed(c.Get(fiber.HeaderOrigin)) {
		return true, respondDeviceBrowserError(c, fiber.StatusForbidden, deviceCodeOriginRejected)
	}
	if len(c.Body()) > deviceBrowserMaxBodyBytes {
		return true, respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeInvalidRequest)
	}
	return false, nil
}

// currentUser reads the session the guard established. The session hash binds
// a claim to the auth-proxy session, or to the Kratos identity when no proxy
// session ID is present.
func (h *deviceBrowserHandlers) currentUser(c fiber.Ctx) (deviceBrowserUser, bool) {
	userID, err := userCoreModule.CurrentUserID(c)
	if err != nil {
		return deviceBrowserUser{}, false
	}
	manager, err := h.store.redisManager()
	if err != nil {
		return deviceBrowserUser{}, false
	}
	session := ""
	if sessionID, ok := c.Locals("authProxySessionID").(string); ok && strings.TrimSpace(sessionID) != "" {
		session = strings.TrimSpace(sessionID)
	} else if identityID, err := userCoreModule.CurrentKratosIdentityID(c); err == nil {
		session = "kratos:" + identityID
	} else {
		session = "user:" + userID
	}
	return deviceBrowserUser{ID: userID, SessionHash: manager.KeyBuilder().HashOAuth2DeviceIdentifier("sess", session)}, true
}

// lookupClient fetches the flow's client from Hydra (the H2 refresh). It is
// not cached: a browser decision must see a client disabled a moment ago.
// nil means Hydra does not know the client.
func (h *deviceBrowserHandlers) lookupClient(ctx context.Context, clientID string) (*HydraOAuthClient, error) {
	client, err := GetHydraOAuthClient(ctx, h.hydraConfig, clientID)
	if err != nil {
		if IsHydraNotFoundError(err) {
			return nil, nil
		}
		return nil, err
	}
	return client, nil
}

// clientStillUsable is the H2 refresh check: the client exists, is enabled,
// holds the device grant, is on the allowlist, and the flow's scope still
// passes the device scope policy.
func (h *deviceBrowserHandlers) clientStillUsable(client *HydraOAuthClient, scope string) bool {
	if !deviceClientUsable(h.cfg, client) {
		return false
	}
	_, ok := checkDeviceScopePolicy(client, normalizeDeviceScope(scope))
	return ok
}

// failForUnusableClient fails a pending or claimed flow whose client became
// unusable and answers client_unavailable.
func (h *deviceBrowserHandlers) failForUnusableClient(c fiber.Ctx, flowID, clientID string) error {
	if _, err := h.store.fail(context.WithoutCancel(c.Context()), flowID); err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	logDeviceEvent(h.logger(), "warn", "chain_error", flowID, clientID, "stage=H2", "reason=client_unavailable")
	return respondDeviceBrowserError(c, fiber.StatusForbidden, deviceCodeClientUnavailable)
}

func (h *deviceBrowserHandlers) userName(ctx context.Context, userID string) (string, error) {
	if h.apiHelper == nil || h.apiHelper.DBManager == nil || h.apiHelper.DBManager.DB == nil {
		return "", errDeviceStoreUnavailable
	}
	user, err := h.apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(userID)).Only(ctx)
	if err != nil {
		return "", err
	}
	return user.Name, nil
}

// counterExceeded counts one event on a never-refusing counter and reports
// whether it is now past limit, with the seconds left in its window.
func (h *deviceBrowserHandlers) counterExceeded(ctx context.Context, key string, window time.Duration, limit int) (bool, int, error) {
	count, err := h.store.increment(ctx, key, window)
	if err != nil {
		return false, 0, err
	}
	if count <= int64(limit) {
		return false, 0, nil
	}
	return true, h.store.retryAfterSeconds(ctx, key, window), nil
}

func newDeviceFlowHandle() string {
	raw := make([]byte, deviceSecretBytes)
	_, _ = rand.Read(raw)
	return deviceFlowHandlePrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func newDeviceFlowNonce() string {
	raw := make([]byte, deviceFlowNonceBytes)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// handleDeviceLookup is POST /api/oauth2/device/lookup: claim a user code
// (design §6.4). Unknown, expired before claim, and taken-by-another-account
// codes all answer invalid_code and count toward the failure budget; a
// malformed code does not.
func (h *deviceBrowserHandlers) handleDeviceLookup(c fiber.Ctx) error {
	if handled, err := h.guard(c); handled {
		return err
	}
	var request deviceLookupRequest
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeInvalidRequest)
	}
	user, ok := h.currentUser(c)
	if !ok {
		return respondDeviceBrowserUnavailable(c)
	}
	ctx := context.WithoutCancel(c.Context())
	limits := h.cfg.Limits()
	keys := h.store.db.Redis.KeyBuilder()

	exceeded, retryAfter, err := h.counterExceeded(ctx, keys.BuildOAuth2DeviceLookupUserKey(user.ID), deviceRateWindow, limits.LookupUserPer10m)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if exceeded {
		return respondDeviceBrowserRateLimited(c, retryAfter)
	}

	userCode, ok := normalizeDeviceUserCode(request.UserCode, h.cfg.UserCodeCharset(), h.cfg.UserCodeLength())
	if !ok {
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeMalformedCode)
	}

	// The failure budget is reserved before any index is read, and given
	// back only when the claim succeeds or the code turns out to be the
	// caller's own.
	counters := []deviceRateCounter{
		{Key: keys.BuildOAuth2DeviceLookupFailUserKey(user.ID), Limit: limits.LookupFailUserPer10m, Window: deviceRateWindow},
		{Key: keys.BuildOAuth2DeviceLookupFailUserDayKey(user.ID), Limit: limits.LookupFailUserPerDay, Window: deviceRateDayWindow},
		{Key: keys.BuildOAuth2DeviceLookupFailGlobalKey(), Limit: limits.LookupFailGlobalPer10m, Window: deviceRateWindow},
	}
	reservation, err := h.store.reserve(ctx, counters)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if reservation.LimitedIndex >= 0 {
		limited := counters[reservation.LimitedIndex]
		return respondDeviceBrowserRateLimited(c, h.store.retryAfterSeconds(ctx, limited.Key, limited.Window))
	}
	if reservation.Counts[2] == int64(max(1, limits.LookupFailGlobalPer10m/deviceLookupGlobalWarnPart)) {
		logDeviceEvent(h.logger(), "warn", "lookup_fail_global_warn", "", "", "count="+strconv.FormatInt(reservation.Counts[2], 10))
	}
	release := func() {
		if err := h.store.release(ctx, counters); err != nil {
			logDeviceEvent(h.logger(), "warn", "lookup_fail", "", "", "reason=release_failed")
		}
	}

	flowHandle := newDeviceFlowHandle()
	claim, err := h.store.claim(ctx, userCode, flowHandle, user.ID, user.SessionHash, h.cfg.claimTTL())
	if err != nil {
		release()
		return respondDeviceBrowserUnavailable(c)
	}
	switch claim.Code {
	case deviceClaimNew, deviceClaimRenewed:
		release()
	case deviceClaimMissing, deviceClaimExpired, deviceClaimTaken:
		logDeviceEvent(h.logger(), "info", "lookup_fail", "", "", "reason="+strings.ToLower(claim.Code))
		if claim.Code == deviceClaimTaken {
			logDeviceEvent(h.logger(), "warn", "lookup_conflict", claim.FlowID, "")
		}
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeInvalidCode)
	case deviceClaimHandledOwn:
		release()
		return respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeAlreadyHandled)
	case deviceClaimInProgressOwn:
		release()
		return respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeFlowConflict)
	case deviceClaimExpiredOwn:
		release()
		return respondDeviceBrowserError(c, fiber.StatusGone, deviceCodeCodeExpired)
	default:
		release()
		return respondDeviceBrowserUnavailable(c)
	}

	// H2 refresh.
	client, err := h.lookupClient(ctx, claim.ClientID)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if !h.clientStillUsable(client, claim.Scope) {
		return h.failForUnusableClient(c, claim.FlowID, claim.ClientID)
	}
	accountName, err := h.userName(ctx, user.ID)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}

	scopes := normalizeDeviceScope(claim.Scope)
	card := deviceReviewCard{
		FlowHandle:  flowHandle,
		UserCode:    formatDeviceUserCode(userCode),
		Scopes:      make([]deviceReviewScope, 0, len(scopes)),
		DeviceLabel: claim.DeviceLabel,
		RequestedAt: claim.CreatedAt.Format(time.RFC3339),
		ExpiresAt:   claim.ExpiresAt.Format(time.RFC3339),
		Account:     deviceReviewAccount{UserID: user.ID, Name: accountName},
	}
	confidential := claim.ClientType == oauthClientTypeConfidential
	card.Client = deviceReviewClient{
		ClientID:   client.ClientID,
		ClientName: client.ClientName,
		ClientType: claim.ClientType,
		// Anyone can use a public client's client_id, so a public client is
		// never shown as first party, and only a confidential initiator was
		// authenticated at device/auth.
		FirstParty:        confidential && HydraClientTypeFromAuthMethod(client.TokenEndpointAuthMethod) == oauthClientTypeConfidential && HydraOAuthClientDevicePolicyOf(client).FirstParty,
		InitiatorVerified: confidential,
	}
	for _, scope := range scopes {
		risk := deviceScopeRisk(scope)
		card.Scopes = append(card.Scopes, deviceReviewScope{Scope: scope, Risk: risk})
		card.WriteWarning = card.WriteWarning || risk == deviceScopeRiskWrite
	}

	userCoreModule.WriteUserAuditLog(c, h.apiHelper, deviceAuditActionClaim, harukiAPIHelper.SystemLogResultSuccess, user.ID, map[string]any{
		"clientID":     claim.ClientID,
		"deviceFlowID": claim.FlowID,
		"scopes":       scopes,
	})
	logDeviceEvent(h.logger(), "info", "claim", claim.FlowID, claim.ClientID, "renewed="+strconv.FormatBool(claim.Code == deviceClaimRenewed))
	return respondDeviceBrowserSuccess(c, fiber.StatusOK, "ok", &card)
}

// respondDeviceDecisionRefusal maps a BeginApprove or Deny refusal to its
// browser answer.
func respondDeviceDecisionRefusal(c fiber.Ctx, code string) error {
	switch code {
	case deviceDecisionHandleMismatch, deviceDecisionInProgress:
		return respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeFlowConflict)
	case deviceDecisionSessionChanged:
		return respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeSessionChanged)
	case deviceDecisionNotClaimer, deviceDecisionHandled:
		return respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeAlreadyHandled)
	case deviceDecisionTooLate, deviceDecisionExpired:
		return respondDeviceBrowserError(c, fiber.StatusGone, deviceCodeCodeExpired)
	case deviceDecisionMaxAttempts:
		return respondDeviceApprovalFailed(c, false)
	}
	return respondDeviceBrowserUnavailable(c)
}

// deviceDefaultLabelZone dates default labels in Asia/Shanghai (UTC+8, no
// DST); a fixed zone avoids depending on the host's tzdata.
var deviceDefaultLabelZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// handleDeviceApprove is POST /api/oauth2/device/approve (design §6.4, §9).
func (h *deviceBrowserHandlers) handleDeviceApprove(c fiber.Ctx) error {
	if handled, err := h.guard(c); handled {
		return err
	}
	var request deviceApproveRequest
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeInvalidRequest)
	}
	if !request.Acknowledged {
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeAckRequired)
	}
	user, ok := h.currentUser(c)
	if !ok {
		return respondDeviceBrowserUnavailable(c)
	}
	ctx := context.WithoutCancel(c.Context())
	if handled, err := h.countDecision(ctx, c, user.ID); handled {
		return err
	}

	flowID, flow, handled, err := h.resolveHandle(ctx, c, request.FlowHandle)
	if handled {
		return err
	}
	userCode, ok := normalizeDeviceUserCode(request.UserCode, h.cfg.UserCodeCharset(), h.cfg.UserCodeLength())
	if !ok {
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeMalformedCode)
	}
	userCodeHash, err := h.store.hashUserCode(userCode)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if subtle.ConstantTimeCompare([]byte(userCodeHash), []byte(flow.UserCodeHash)) != 1 {
		return respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeFlowConflict)
	}
	label, labelSource := "", ""
	if request.Label != "" {
		if label = sanitizeDeviceLabel(request.Label); label == "" {
			return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeInvalidRequest)
		}
		labelSource = deviceLabelSourceUser
	}

	// H2 refresh.
	client, err := h.lookupClient(ctx, flow.ClientID)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if !h.clientStillUsable(client, flow.Scope) {
		return h.failForUnusableClient(c, flowID, flow.ClientID)
	}
	clientName := strings.TrimSpace(client.ClientName)
	if clientName == "" {
		clientName = client.ClientID
	}
	switch {
	case label != "":
	case flow.DeviceLabel != "":
		label, labelSource = flow.DeviceLabel, deviceLabelSourceDevice
	default:
		label = clientName + " · " + h.store.now().In(deviceDefaultLabelZone).Format(time.DateOnly)
		labelSource = deviceLabelSourceDefault
	}

	subject, err := CurrentHydraSubject(c)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if h.apiHelper == nil || h.apiHelper.DBManager == nil || h.apiHelper.DBManager.DB == nil {
		return respondDeviceBrowserUnavailable(c)
	}
	dbUser, err := h.apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(user.ID)).Only(ctx)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}

	// H9a: the CAS, before any Hydra browser-leg call.
	timings := h.cfg.approvalTimings()
	nonce := newDeviceFlowNonce()
	begin, attempt, err := h.store.beginApprove(ctx, flowID, deviceDecisionActor{UserID: user.ID, SessionHash: user.SessionHash, FlowHandle: request.FlowHandle}, timings, nonce)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if begin != deviceDecisionOK {
		return respondDeviceDecisionRefusal(c, begin)
	}

	scopes := normalizeDeviceScope(flow.Scope)
	chain := &deviceApprovalChain{
		hydra:           h.hydraConfig,
		cfg:             h.cfg,
		store:           h.store,
		logger:          h.logger(),
		flowID:          flowID,
		clientID:        flow.ClientID,
		scopes:          scopes,
		userCode:        userCode,
		subject:         subject,
		nonce:           nonce,
		attempt:         attempt,
		finalHopTimeout: deviceChainFollowUpTimeout,
		consentBody: buildHydraConsentAcceptBody(hydraConsentAcceptBodyInput{
			GrantScope:    scopes,
			GrantAudience: []string{},
			UserID:        dbUser.ID,
			UserName:      dbUser.Name,
			UserEmail:     dbUser.Email,
			EmailVerified: userCoreModule.IsCurrentUserEmailVerified(c),
			Device:        &hydraConsentDeviceContext{FlowID: flowID, Label: label, LabelSource: labelSource},
		}),
	}
	chainCtx, cancel := context.WithTimeout(ctx, timings.ApprovalTimeout)
	result := chain.run(chainCtx)
	cancel()

	outcome := deviceApproveOutcome{
		handlers: h, c: c, flowID: flowID, clientID: flow.ClientID, nonce: nonce, user: user,
		label: label, labelSource: labelSource, scopes: scopes, attempt: attempt, result: result,
		clientName: clientName, accountName: dbUser.Name, maxAttempts: timings.MaxApproveAttempts,
	}
	return outcome.respond()
}

// deviceApproveOutcome settles a finished chain in Redis and answers it.
type deviceApproveOutcome struct {
	handlers    *deviceBrowserHandlers
	c           fiber.Ctx
	flowID      string
	clientID    string
	nonce       string
	user        deviceBrowserUser
	label       string
	labelSource string
	scopes      []string
	attempt     int
	maxAttempts int
	clientName  string
	accountName string
	result      deviceChainResult
}

func (o *deviceApproveOutcome) followUpContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(o.c.Context()), deviceChainFollowUpTimeout)
}

// finish ends the attempt in Redis and returns the result code and state.
func (o *deviceApproveOutcome) finish(mode string) (string, string) {
	ctx, cancel := o.followUpContext()
	defer cancel()
	code, state, err := o.handlers.store.finishApprove(ctx, o.flowID, o.nonce, mode, o.label, o.labelSource, o.maxAttempts)
	if err != nil {
		logDeviceEvent(o.handlers.logger(), "error", "chain_error", o.flowID, o.clientID, "stage=H9k", "reason="+deviceChainReasonStore)
		return "", ""
	}
	return code, state
}

func (o *deviceApproveOutcome) audit(result, reason string) {
	o.handlers.logResult(o.flowID, o.clientID, result, o.result)
	userCoreModule.WriteUserAuditLog(o.c, o.handlers.apiHelper, deviceAuditActionApprove, auditResultOf(result), o.user.ID, map[string]any{
		"clientID":         o.clientID,
		"deviceFlowID":     o.flowID,
		"result":           result,
		"reason":           reason,
		"scopes":           o.scopes,
		"label":            o.label,
		"consentRequestId": o.result.ConsentRequestID,
		"attempt":          o.attempt,
	})
}

func auditResultOf(result string) string {
	if result == deviceFinishApproved {
		return harukiAPIHelper.SystemLogResultSuccess
	}
	return harukiAPIHelper.SystemLogResultFailure
}

func (o *deviceApproveOutcome) approved() error {
	o.audit(deviceFinishApproved, "")
	return respondDeviceBrowserSuccess(o.c, fiber.StatusOK, "device approved", &deviceApproveResponse{
		Status:           deviceFlowStateApproved,
		ClientName:       o.clientName,
		ConsentRequestID: o.result.ConsentRequestID,
		AccountName:      o.accountName,
	})
}

func (o *deviceApproveOutcome) unconfirmed(reason string) error {
	o.audit(deviceFinishUnconfirmed, reason)
	return respondDeviceBrowserSuccess(o.c, fiber.StatusAccepted, "device approval outcome unknown", &deviceApproveResponse{Status: deviceFlowStateUnconfirmed})
}

func (o *deviceApproveOutcome) respond() error {
	result := o.result
	reason := result.Stage + ":" + result.Reason
	switch result.Outcome {
	case deviceChainApproved:
		switch code, _ := o.finish(deviceFinishApproved); code {
		case deviceDecisionOK, deviceFinishAlreadyIssued:
			return o.approved()
		default:
			// Hydra completed the consent but this attempt could not be
			// recorded as approved; the device may still redeem it.
			return o.unconfirmed("H9k:finish_failed")
		}
	case deviceChainUnconfirmed:
		if code, _ := o.finish(deviceFinishUnconfirmed); code == deviceFinishAlreadyIssued {
			return o.approved()
		}
		return o.unconfirmed(reason)
	case deviceChainRetryable, deviceChainStoreError:
		code, state := o.finish(deviceFinishRevert)
		if code == deviceFinishAlreadyIssued {
			return o.approved()
		}
		o.audit(deviceFlowStateFailed, reason)
		if result.Outcome == deviceChainStoreError {
			return respondDeviceBrowserUnavailable(o.c)
		}
		return respondDeviceApprovalFailed(o.c, state != deviceFlowStateFailed)
	case deviceChainNonceLost:
		o.audit(deviceFlowStateFailed, reason)
		return respondDeviceBrowserError(o.c, fiber.StatusConflict, deviceCodeFlowConflict)
	case deviceChainFailedAfterConsent:
		if code, _ := o.finish(deviceFinishFailed); code == deviceFinishAlreadyIssued {
			return o.approved()
		}
		o.revokeRecordedConsent()
		o.audit(deviceFlowStateFailed, reason)
		return respondDeviceApprovalFailed(o.c, false)
	default: // failed, login skip, code expired
		if code, _ := o.finish(deviceFinishFailed); code == deviceFinishAlreadyIssued {
			return o.approved()
		}
		o.audit(deviceFlowStateFailed, reason)
		if result.Outcome == deviceChainCodeExpired {
			return respondDeviceBrowserError(o.c, fiber.StatusGone, deviceCodeCodeExpired)
		}
		return respondDeviceApprovalFailed(o.c, false)
	}
}

// revokeRecordedConsent revokes the consent of an attempt that failed after
// Hydra may have completed it; the reaper retries when this fails.
func (o *deviceApproveOutcome) revokeRecordedConsent() {
	if o.result.ConsentRequestID == "" {
		return
	}
	ctx, cancel := o.followUpContext()
	defer cancel()
	if err := RevokeHydraConsentSessionByID(ctx, o.handlers.hydraConfig, o.result.ConsentRequestID); err != nil {
		logDeviceEvent(o.handlers.logger(), "warn", "chain_error", o.flowID, o.clientID, "stage=H9j", "reason="+deviceChainReasonRevoke)
		return
	}
	if err := o.handlers.store.removeUnredeemed(ctx, o.flowID); err != nil {
		logDeviceEvent(o.handlers.logger(), "warn", "chain_error", o.flowID, o.clientID, "stage=H9j", "reason=unredeemed_remove_failed")
	}
}

// logResult writes the structured line of an approve: approve on success,
// unconfirmed, login_skip_unexpected, or chain_error.
func (h *deviceBrowserHandlers) logResult(flowID, clientID, result string, chain deviceChainResult) {
	fields := []string{"result=" + result}
	if chain.Stage != "" {
		fields = append(fields, "stage="+chain.Stage)
	}
	if chain.Reason != "" {
		fields = append(fields, "reason="+chain.Reason)
	}
	if chain.Status != 0 {
		fields = append(fields, "status="+strconv.Itoa(chain.Status))
	}
	switch {
	case result == deviceFinishApproved:
		logDeviceEvent(h.logger(), "info", "approve", flowID, clientID, fields...)
	case result == deviceFinishUnconfirmed:
		logDeviceEvent(h.logger(), "warn", "unconfirmed", flowID, clientID, fields...)
	case chain.Outcome == deviceChainLoginSkip:
		logDeviceEvent(h.logger(), "error", "login_skip_unexpected", flowID, clientID, fields...)
	default:
		logDeviceEvent(h.logger(), "error", "chain_error", flowID, clientID, fields...)
	}
}

// countDecision counts one approve or deny against decision:user-day.
func (h *deviceBrowserHandlers) countDecision(ctx context.Context, c fiber.Ctx, userID string) (bool, error) {
	exceeded, retryAfter, err := h.counterExceeded(ctx, h.store.db.Redis.KeyBuilder().BuildOAuth2DeviceDecisionUserDayKey(userID), deviceRateDayWindow, h.cfg.Limits().DecisionUserPerDay)
	if err != nil {
		return true, respondDeviceBrowserUnavailable(c)
	}
	if exceeded {
		return true, respondDeviceBrowserRateLimited(c, retryAfter)
	}
	return false, nil
}

// resolveHandle finds the flow behind a flow handle. An unknown, expired or
// replaced handle is flow_conflict: handles are 256-bit and unguessable, so
// this is not a probe.
func (h *deviceBrowserHandlers) resolveHandle(ctx context.Context, c fiber.Ctx, flowHandle string) (string, deviceFlowSnapshot, bool, error) {
	if !strings.HasPrefix(flowHandle, deviceFlowHandlePrefix) {
		return "", deviceFlowSnapshot{}, true, respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeFlowConflict)
	}
	flowID, found, err := h.store.flowIDForHandle(ctx, flowHandle)
	if err != nil {
		return "", deviceFlowSnapshot{}, true, respondDeviceBrowserUnavailable(c)
	}
	if !found {
		return "", deviceFlowSnapshot{}, true, respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeFlowConflict)
	}
	flow, found, err := h.store.snapshot(ctx, flowID)
	if err != nil {
		return "", deviceFlowSnapshot{}, true, respondDeviceBrowserUnavailable(c)
	}
	if !found {
		return "", deviceFlowSnapshot{}, true, respondDeviceBrowserError(c, fiber.StatusConflict, deviceCodeFlowConflict)
	}
	return flowID, flow, false, nil
}

// handleDeviceDeny is POST /api/oauth2/device/deny. It never calls Hydra's
// reject (Hydra keeps nothing of it and the device would stay pending); the
// denial lives in Redis and the token endpoint answers access_denied. A flow
// with a recorded consent (an approve whose lease ran out, or a reverted
// attempt) has that consent revoked, since Hydra may have completed it.
func (h *deviceBrowserHandlers) handleDeviceDeny(c fiber.Ctx) error {
	if handled, err := h.guard(c); handled {
		return err
	}
	var request deviceDenyRequest
	if err := json.Unmarshal(c.Body(), &request); err != nil {
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeInvalidRequest)
	}
	switch request.Reason {
	case "":
		request.Reason = deviceDenyReasonUser
	case deviceDenyReasonUser, deviceDenyReasonNotMine:
	default:
		return respondDeviceBrowserError(c, fiber.StatusBadRequest, deviceCodeInvalidRequest)
	}
	user, ok := h.currentUser(c)
	if !ok {
		return respondDeviceBrowserUnavailable(c)
	}
	ctx := context.WithoutCancel(c.Context())
	if handled, err := h.countDecision(ctx, c, user.ID); handled {
		return err
	}
	flowID, flow, handled, err := h.resolveHandle(ctx, c, request.FlowHandle)
	if handled {
		return err
	}
	code, consentRequestID, err := h.store.deny(ctx, flowID, deviceDecisionActor{UserID: user.ID, SessionHash: user.SessionHash, FlowHandle: request.FlowHandle}, request.Reason)
	if err != nil {
		return respondDeviceBrowserUnavailable(c)
	}
	if code != deviceDecisionOK {
		return respondDeviceDecisionRefusal(c, code)
	}

	revoked := false
	if consentRequestID != "" {
		revokeCtx, cancel := context.WithTimeout(ctx, deviceChainFollowUpTimeout)
		if err := RevokeHydraConsentSessionByID(revokeCtx, h.hydraConfig, consentRequestID); err != nil {
			// The denial stands; the token endpoint and the reaper revoke later.
			logDeviceEvent(h.logger(), "warn", "deny", flowID, flow.ClientID, "reason="+deviceChainReasonRevoke)
		} else {
			revoked = true
			if err := h.store.removeUnredeemed(revokeCtx, flowID); err != nil {
				logDeviceEvent(h.logger(), "warn", "deny", flowID, flow.ClientID, "reason=unredeemed_remove_failed")
			}
		}
		cancel()
	}
	logDeviceEvent(h.logger(), "info", "deny", flowID, flow.ClientID, "reason="+request.Reason, "revoked="+strconv.FormatBool(revoked))
	if request.Reason == deviceDenyReasonNotMine {
		logDeviceEvent(h.logger(), "warn", "phishing_signal", flowID, flow.ClientID)
	}
	userCoreModule.WriteUserAuditLog(c, h.apiHelper, deviceAuditActionDeny, harukiAPIHelper.SystemLogResultSuccess, user.ID, map[string]any{
		"clientID":     flow.ClientID,
		"deviceFlowID": flowID,
		"reason":       request.Reason,
	})
	return respondDeviceBrowserSuccess(c, fiber.StatusOK, "device denied", &deviceDenyResponse{Status: deviceFlowStateDenied})
}
