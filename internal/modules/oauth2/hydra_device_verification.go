package oauth2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// The server-driven device approval chain (design §9). Inside one approve
// request the backend walks Hydra's whole browser leg on the user's behalf:
//
//	H9b GET  verify?haruki_dfl=F          → 302 FE/device?device_challenge=X
//	H9c PUT  admin device/accept           → redirect_to issuer verify (device_verifier)
//	H9d GET  verify (rewritten)            → 302 FE/oauth2/login?login_challenge=L
//	H9e GET  admin login request           (skip, client, marker, scope checks)
//	H9f PUT  admin login/accept            → redirect_to issuer verify (login_verifier)
//	H9g GET  verify (rewritten)            → 302 FE/oauth2/consent?consent_challenge=K
//	H9h GET  admin consent request; RecordConsent in Redis
//	H9i PUT  admin consent/accept          → redirect_to issuer verify (consent_verifier)
//	H9j GET  verify (rewritten)            → 302 FE/device/done?client_id=C (never followed)
//
// Each approval gets a fresh in-memory cookie jar that is dropped when the
// handler returns. Hydra's cookies are Secure in non-dev mode, and the chain
// talks to Hydra over plain internal http, so a standard cookiejar would drop
// them; the jar replays them by name. The only URL ever rewritten is an
// absolute issuer verify URL; frontend Locations are parsed, never requested.
// The flow marker haruki_dfl=<fid>, set on the first verify (which never
// carries user_code), binds every login and consent request to this flow.
// Nothing here logs a URL, challenge, verifier, cookie or user code.

const (
	deviceFlowMarkerParam = "haruki_dfl"
	// deviceChainPublicBodyLimit bounds what is read of a Hydra public
	// response; the chain only needs its status, Location and cookies.
	deviceChainPublicBodyLimit = 64 << 10
	// deviceChainFollowUpTimeout bounds the final-hop retry and the
	// revocations and Redis writes that follow a chain, each separately.
	deviceChainFollowUpTimeout   = 5 * time.Second
	hydraDeviceCSRFCookiePrefix  = "ory_hydra_device_csrf"
	hydraDeviceAcceptEndpoint    = "/admin/oauth2/auth/requests/device/accept"
	hydraLoginAcceptEndpoint     = "/admin/oauth2/auth/requests/login/accept"
	hydraConsentAcceptEndpoint   = "/admin/oauth2/auth/requests/consent/accept"
	frontendDevicePath           = "/device"
	frontendDeviceDonePath       = "/device/done"
	frontendOAuthLoginPath       = "/oauth2/login"
	frontendOAuthConsentPath     = "/oauth2/consent"
	deviceChainReasonUnexpected  = "unexpected_redirect"
	deviceChainReasonTransport   = "transport"
	deviceChainReasonStatus      = "status"
	deviceChainReasonMarker      = "marker_mismatch"
	deviceChainReasonClient      = "client_mismatch"
	deviceChainReasonScope       = "scope_mismatch"
	deviceChainReasonAudience    = "audience_present"
	deviceChainReasonSubject     = "subject_mismatch"
	deviceChainReasonLoginSkip   = "login_skip"
	deviceChainReasonNoCSRF      = "csrf_cookie_missing"
	deviceChainReasonNoConsentID = "consent_request_id_missing"
	deviceChainReasonAccept400   = "device_accept_rejected"
	deviceChainReasonChallenge   = "challenge_unknown"
	deviceChainReasonStore       = "store_unavailable"
	deviceChainReasonNonce       = "nonce_mismatch"
	deviceChainReasonFinalHop    = "final_hop_unknown"
	deviceChainReasonRevoke      = "revoke_failed"
)

// errUnexpectedHydraRedirect is a Location, redirect_to or request_url the
// chain does not accept. The chain fails closed on it.
var errUnexpectedHydraRedirect = errors.New("unexpected hydra redirect")

// deviceCookieJar is the per-approval cookie jar: name → value. It ignores
// Secure, Domain, Path, SameSite and HttpOnly, because every request it serves
// goes to the one Hydra verify endpoint over the internal network.
type deviceCookieJar map[string]string

// absorb stores the cookies a Hydra public response sets and drops the ones it
// expires.
func (j deviceCookieJar) absorb(resp *http.Response, now time.Time) {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "" {
			continue
		}
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(now)) {
			delete(j, cookie.Name)
			continue
		}
		j[cookie.Name] = cookie.Value
	}
}

// header joins every live cookie into one Cookie header value, in name order.
func (j deviceCookieJar) header() string {
	names := make([]string, 0, len(j))
	for name := range j {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+j[name])
	}
	return strings.Join(parts, "; ")
}

func (j deviceCookieJar) hasPrefix(prefix string) bool {
	for name := range j {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// deviceChainOutcome is how one approve request's chain ended.
type deviceChainOutcome int

const (
	// deviceChainApproved: H9j answered the expected 302.
	deviceChainApproved deviceChainOutcome = iota
	// deviceChainRetryable: a definite failure while Hydra holds no completed
	// consent; the flow goes back to claimed (failed once out of attempts).
	deviceChainRetryable
	// deviceChainFailed: a validation mismatch; the flow fails.
	deviceChainFailed
	// deviceChainLoginSkip: Hydra offered a login skip; the flow fails.
	deviceChainLoginSkip
	// deviceChainCodeExpired: device accept answered 400 on the first attempt.
	deviceChainCodeExpired
	// deviceChainUnconfirmed: Hydra may or may not have completed the consent.
	deviceChainUnconfirmed
	// deviceChainFailedAfterConsent: H9j redirected somewhere unexpected; the
	// flow fails and its recorded consent is revoked.
	deviceChainFailedAfterConsent
	// deviceChainNonceLost: RecordConsent found another attempt's nonce.
	deviceChainNonceLost
	// deviceChainStoreError: Redis failed during the chain.
	deviceChainStoreError
)

type deviceChainResult struct {
	Outcome          deviceChainOutcome
	Stage            string
	Reason           string
	Status           int
	ConsentRequestID string
}

// deviceApprovalChain carries one approval attempt's inputs.
type deviceApprovalChain struct {
	hydra       *harukiOAuth2.HydraConfig
	cfg         DeviceFlowConfig
	store       *deviceFlowStore
	logger      *harukiLogger.Logger
	flowID      string
	clientID    string
	scopes      []string
	userCode    string
	subject     string
	nonce       string
	attempt     int
	consentBody map[string]any
	// finalHopTimeout bounds the H9j retry and the revocation after an
	// explicit H9j failure, each with its own context.
	finalHopTimeout time.Duration
}

// run executes the chain: H9b–H9j, restarting once from H9b with a new jar
// when device accept says the challenge is unknown or too old (404/401).
func (ch *deviceApprovalChain) run(ctx context.Context) deviceChainResult {
	result, restart := ch.attemptOnce(ctx)
	if !restart {
		return result
	}
	logDeviceEvent(ch.logger, "warn", "chain_error", ch.flowID, ch.clientID, "stage=H9c", "reason="+deviceChainReasonChallenge, "status="+strconv.Itoa(result.Status), "restart=true")
	result, restart = ch.attemptOnce(ctx)
	if restart {
		return ch.retryable("H9c", deviceChainReasonChallenge, result.Status)
	}
	return result
}

func (ch *deviceApprovalChain) result(outcome deviceChainOutcome, stage, reason string, status int) deviceChainResult {
	return deviceChainResult{Outcome: outcome, Stage: stage, Reason: reason, Status: status}
}

func (ch *deviceApprovalChain) retryable(stage, reason string, status int) deviceChainResult {
	return ch.result(deviceChainRetryable, stage, reason, status)
}

func (ch *deviceApprovalChain) failed(stage, reason string) deviceChainResult {
	return ch.result(deviceChainFailed, stage, reason, 0)
}

// attemptOnce walks H9b–H9j with a new jar. restart reports a device accept
// 401/404, after which the caller may start over once.
func (ch *deviceApprovalChain) attemptOnce(ctx context.Context) (deviceChainResult, bool) {
	jar := deviceCookieJar{}
	verifyEndpoint, err := ch.hydra.PublicEndpoint(hydraDeviceVerifyPath)
	if err != nil {
		return ch.retryable("H9b", deviceChainReasonTransport, 0), false
	}

	// H9b: the flow marker only, never user_code.
	status, location, err := ch.publicGet(ctx, verifyEndpoint+"?"+url.Values{deviceFlowMarkerParam: {ch.flowID}}.Encode(), jar)
	if err != nil {
		return ch.retryable("H9b", deviceChainReasonTransport, 0), false
	}
	if status != http.StatusFound {
		return ch.retryable("H9b", deviceChainReasonStatus, status), false
	}
	query, err := ch.frontendLocation(location, frontendDevicePath)
	if err != nil {
		return ch.failed("H9b", deviceChainReasonUnexpected), false
	}
	deviceChallenge, ok := singleQueryValue(query, "device_challenge")
	if !ok {
		return ch.failed("H9b", deviceChainReasonUnexpected), false
	}
	if !jar.hasPrefix(hydraDeviceCSRFCookiePrefix) {
		return ch.retryable("H9b", deviceChainReasonNoCSRF, status), false
	}

	// H9c: the normalized user code goes only into this admin JSON body.
	accepted, err := sendHydraAdminJSON(ctx, ch.hydra, http.MethodPut, hydraDeviceAcceptEndpoint,
		url.Values{"device_challenge": {deviceChallenge}}, map[string]any{"user_code": ch.userCode})
	if err != nil {
		switch adminStatus := HydraRequestStatusCode(err); adminStatus {
		case http.StatusBadRequest:
			// The code is unknown, expired or already used. On a first attempt
			// nothing can have been completed, so the code is simply expired;
			// on a later one an earlier attempt may have completed in Hydra.
			if ch.attempt <= 1 {
				return ch.result(deviceChainCodeExpired, "H9c", deviceChainReasonAccept400, adminStatus), false
			}
			return ch.result(deviceChainUnconfirmed, "H9c", deviceChainReasonAccept400, adminStatus), false
		case http.StatusUnauthorized, http.StatusNotFound:
			return ch.retryable("H9c", deviceChainReasonChallenge, adminStatus), true
		default:
			return ch.retryable("H9c", deviceChainReasonTransport, adminStatus), false
		}
	}
	next, err := ch.issuerVerifyURL(accepted.RedirectTo, "device_verifier")
	if err != nil {
		return ch.failed("H9c", reasonOf(err)), false
	}

	// H9d.
	if status, location, err = ch.publicGet(ctx, next, jar); err != nil {
		return ch.retryable("H9d", deviceChainReasonTransport, 0), false
	}
	if status != http.StatusFound {
		logDeviceEvent(ch.logger, "error", "chain_error", ch.flowID, ch.clientID, "stage=H9d", "reason="+deviceChainReasonStatus, "status="+strconv.Itoa(status))
		return ch.retryable("H9d", deviceChainReasonStatus, status), false
	}
	if query, err = ch.frontendLocation(location, frontendOAuthLoginPath); err != nil {
		return ch.failed("H9d", deviceChainReasonUnexpected), false
	}
	loginChallenge, ok := singleQueryValue(query, "login_challenge")
	if !ok {
		return ch.failed("H9d", deviceChainReasonUnexpected), false
	}

	// H9e.
	loginReq, err := getHydraLoginRequest(ctx, ch.hydra, loginChallenge)
	if err != nil {
		return ch.retryable("H9e", deviceChainReasonTransport, HydraRequestStatusCode(err)), false
	}
	if loginReq.Skip {
		// A remembered login would carry someone else's subject. It is never
		// accepted, and the subject is not echoed anywhere.
		return ch.result(deviceChainLoginSkip, "H9e", deviceChainReasonLoginSkip, 0), false
	}
	if reason := ch.checkRequest(loginReq.Client.ClientID, loginReq.RequestURL, loginReq.RequestedScope, loginReq.RequestedAccessTokenAudience); reason != "" {
		return ch.failed("H9e", reason), false
	}

	// H9f: exactly subject, remember=false, remember_for=0; never acr or context.
	loginAccepted, err := sendHydraAdminJSON(ctx, ch.hydra, http.MethodPut, hydraLoginAcceptEndpoint,
		url.Values{"login_challenge": {loginChallenge}}, map[string]any{"subject": ch.subject, "remember": false, "remember_for": 0})
	if err != nil {
		return ch.retryable("H9f", deviceChainReasonTransport, HydraRequestStatusCode(err)), false
	}
	if next, err = ch.issuerVerifyURL(loginAccepted.RedirectTo, "login_verifier"); err != nil {
		return ch.failed("H9f", reasonOf(err)), false
	}

	// H9g.
	if status, location, err = ch.publicGet(ctx, next, jar); err != nil {
		return ch.retryable("H9g", deviceChainReasonTransport, 0), false
	}
	if status != http.StatusFound {
		return ch.retryable("H9g", deviceChainReasonStatus, status), false
	}
	if query, err = ch.frontendLocation(location, frontendOAuthConsentPath); err != nil {
		return ch.failed("H9g", deviceChainReasonUnexpected), false
	}
	consentChallenge, ok := singleQueryValue(query, "consent_challenge")
	if !ok {
		return ch.failed("H9g", deviceChainReasonUnexpected), false
	}

	// H9h: validate, then record the consent request ID before accepting.
	consentReq, err := getHydraConsentRequest(ctx, ch.hydra, consentChallenge)
	if err != nil {
		return ch.retryable("H9h", deviceChainReasonTransport, HydraRequestStatusCode(err)), false
	}
	if strings.TrimSpace(consentReq.Subject) != ch.subject {
		return ch.failed("H9h", deviceChainReasonSubject), false
	}
	if reason := ch.checkRequest(consentReq.Client.ClientID, consentReq.RequestURL, consentReq.RequestedScope, consentReq.RequestedAccessTokenAudience); reason != "" {
		return ch.failed("H9h", reason), false
	}
	consentRequestID := strings.TrimSpace(consentReq.ConsentRequestID)
	if consentRequestID == "" {
		return ch.failed("H9h", deviceChainReasonNoConsentID), false
	}
	if consentReq.Skip {
		// A confidential client's remembered consent; the chain decides anyway.
		logDeviceEvent(ch.logger, "info", "approve", ch.flowID, ch.clientID, "stage=H9h", "consent_skip=true")
	}
	recorded, err := ch.store.recordConsent(ctx, ch.flowID, ch.nonce, consentRequestID, ch.subject)
	if err != nil {
		return ch.result(deviceChainStoreError, "H9h", deviceChainReasonStore, 0), false
	}
	if !recorded {
		return ch.result(deviceChainNonceLost, "H9h", deviceChainReasonNonce, 0), false
	}

	// H9i.
	consentAccepted, err := sendHydraAdminJSON(ctx, ch.hydra, http.MethodPut, hydraConsentAcceptEndpoint,
		url.Values{"consent_challenge": {consentChallenge}}, ch.consentBody)
	if err != nil {
		result := ch.retryable("H9i", deviceChainReasonTransport, HydraRequestStatusCode(err))
		result.ConsentRequestID = consentRequestID
		return result, false
	}
	if next, err = ch.issuerVerifyURL(consentAccepted.RedirectTo, "consent_verifier"); err != nil {
		result := ch.failed("H9i", reasonOf(err))
		result.ConsentRequestID = consentRequestID
		return result, false
	}

	// H9j.
	result := ch.finalHop(ctx, next, jar, consentRequestID)
	result.ConsentRequestID = consentRequestID
	return result, false
}

// finalHop runs H9j. The expected 302 is only checked, never followed.
//   - Unknown result (timeout, transport error): retry once with the same jar
//     under its own short timeout; a 302 there is success, anything else is
//     unconfirmed (a 403 "verifier already used" means the first try
//     completed).
//   - An explicit non-302 on the first try: Hydra may have written the consent
//     session before failing, so it is revoked first; only after Hydra
//     confirmed the revocation does the flow go back to claimed.
func (ch *deviceApprovalChain) finalHop(ctx context.Context, target string, jar deviceCookieJar, consentRequestID string) deviceChainResult {
	status, location, err := ch.publicGet(ctx, target, jar)
	if err != nil {
		retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ch.finalHopTimeout)
		defer cancel()
		status, location, err = ch.publicGet(retryCtx, target, jar)
		if err == nil && status == http.StatusFound && ch.deviceDoneLocation(location) == nil {
			return ch.result(deviceChainApproved, "H9j", "", status)
		}
		return ch.result(deviceChainUnconfirmed, "H9j", deviceChainReasonFinalHop, status)
	}
	if status == http.StatusFound {
		if ch.deviceDoneLocation(location) != nil {
			return ch.result(deviceChainFailedAfterConsent, "H9j", deviceChainReasonUnexpected, status)
		}
		return ch.result(deviceChainApproved, "H9j", "", status)
	}
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ch.finalHopTimeout)
	defer cancel()
	if err := RevokeHydraConsentSessionByID(revokeCtx, ch.hydra, consentRequestID); err != nil {
		return ch.result(deviceChainUnconfirmed, "H9j", deviceChainReasonRevoke, status)
	}
	return ch.retryable("H9j", deviceChainReasonStatus, status)
}

// publicGet sends one GET to Hydra's public verify endpoint without following
// redirects, with the jar's cookies when it has any, and absorbs the cookies
// of the answer. It returns the status and the Location header.
func (ch *deviceApprovalChain) publicGet(ctx context.Context, target string, jar deviceCookieJar) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, "", err
	}
	if cookies := jar.header(); cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	resp, err := ch.hydra.DoWithoutRedirect(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, deviceChainPublicBodyLimit)); err != nil {
		return 0, "", err
	}
	jar.absorb(resp, ch.store.now())
	return resp.StatusCode, resp.Header.Get("Location"), nil
}

// frontendLocation parses a Hydra Location that must point at the frontend
// page wantPath. It is only parsed, never requested.
func (ch *deviceApprovalChain) frontendLocation(location, wantPath string) (url.Values, error) {
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return nil, errUnexpectedHydraRedirect
	}
	origin := urlOrigin(parsed.String())
	if origin == "" || origin != ch.cfg.FrontendOrigin() || parsed.Path != wantPath {
		return nil, errUnexpectedHydraRedirect
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || forbiddenChainParams(query) {
		return nil, errUnexpectedHydraRedirect
	}
	return query, nil
}

// deviceDoneLocation checks H9j's Location: FE/device/done?client_id=C.
func (ch *deviceApprovalChain) deviceDoneLocation(location string) error {
	query, err := ch.frontendLocation(location, frontendDeviceDonePath)
	if err != nil {
		return err
	}
	if clientID, ok := singleQueryValue(query, "client_id"); !ok || clientID != ch.clientID {
		return errUnexpectedHydraRedirect
	}
	return nil
}

// deviceChainMismatch carries the enumerated reason of a rejected URL.
type deviceChainMismatch struct{ reason string }

func (e *deviceChainMismatch) Error() string { return "device chain mismatch: " + e.reason }
func (e *deviceChainMismatch) Unwrap() error { return errUnexpectedHydraRedirect }

func reasonOf(err error) string {
	var mismatch *deviceChainMismatch
	if errors.As(err, &mismatch) {
		return mismatch.reason
	}
	return deviceChainReasonUnexpected
}

// checkIssuerVerify validates an absolute URL that must be the issuer's verify
// address carrying the flow marker and the flow's client, and returns its
// query.
func (ch *deviceApprovalChain) checkIssuerVerify(raw string) (url.Values, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Fragment != "" || ch.cfg.HydraIssuerOrigin() == "" {
		return nil, "", &deviceChainMismatch{reason: deviceChainReasonUnexpected}
	}
	if urlOrigin(parsed.String()) != ch.cfg.HydraIssuerOrigin() || parsed.Path != ch.cfg.HydraIssuerPath()+hydraDeviceVerifyPath {
		return nil, "", &deviceChainMismatch{reason: deviceChainReasonUnexpected}
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || forbiddenChainParams(query) {
		return nil, "", &deviceChainMismatch{reason: deviceChainReasonUnexpected}
	}
	if marker, ok := singleQueryValue(query, deviceFlowMarkerParam); !ok || marker != ch.flowID {
		return nil, "", &deviceChainMismatch{reason: deviceChainReasonMarker}
	}
	if clientID, ok := singleQueryValue(query, "client_id"); !ok || clientID != ch.clientID {
		return nil, "", &deviceChainMismatch{reason: deviceChainReasonClient}
	}
	return query, parsed.RawQuery, nil
}

// issuerVerifyURL validates a redirect_to that must be the issuer's verify
// address with the given verifier parameter, and rewrites it to Hydra's
// internal public endpoint. This is the only URL rewrite the chain does.
func (ch *deviceApprovalChain) issuerVerifyURL(redirectTo, verifierParam string) (string, error) {
	query, rawQuery, err := ch.checkIssuerVerify(redirectTo)
	if err != nil {
		return "", err
	}
	if _, ok := singleQueryValue(query, verifierParam); !ok {
		return "", &deviceChainMismatch{reason: deviceChainReasonUnexpected}
	}
	return rewriteIssuerVerifyURL(ch.hydra, rawQuery)
}

// rewriteIssuerVerifyURL points an issuer verify URL's query at Hydra's
// internal public verify endpoint.
func rewriteIssuerVerifyURL(hydra *harukiOAuth2.HydraConfig, rawQuery string) (string, error) {
	endpoint, err := hydra.PublicEndpoint(hydraDeviceVerifyPath)
	if err != nil {
		return "", &deviceChainMismatch{reason: deviceChainReasonUnexpected}
	}
	return endpoint + "?" + rawQuery, nil
}

// checkRequest validates a login or consent request against the flow: client,
// request_url (issuer verify address with marker and client), requested scope
// as a set, and no audience. It returns an enumerated reason, or "".
func (ch *deviceApprovalChain) checkRequest(clientID, requestURL string, scopes, audience []string) string {
	if strings.TrimSpace(clientID) != ch.clientID {
		return deviceChainReasonClient
	}
	if _, _, err := ch.checkIssuerVerify(requestURL); err != nil {
		return reasonOf(err)
	}
	if !slices.Equal(normalizeDeviceScope(strings.Join(scopes, " ")), ch.scopes) {
		return deviceChainReasonScope
	}
	if len(audience) > 0 {
		return deviceChainReasonAudience
	}
	return ""
}

// forbiddenChainParams reports a query the chain refuses anywhere: a user_code
// (the first verify never carries one, so it must not come back) or a prompt.
func forbiddenChainParams(query url.Values) bool {
	_, hasUserCode := query["user_code"]
	_, hasPrompt := query["prompt"]
	return hasUserCode || hasPrompt
}

// singleQueryValue returns the one non-empty value of name.
func singleQueryValue(query url.Values, name string) (string, bool) {
	values := query[name]
	if len(values) != 1 || values[0] == "" {
		return "", false
	}
	return values[0], true
}
