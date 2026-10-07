package oauth2

import (
	"context"
	"net/url"
	"strings"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

// hydraDeviceVerifyPath is the Hydra public path that starts the browser leg of
// an RFC 8628 device flow. Hydra records that URL (with the user code masked)
// as the request_url of every login and consent request the flow creates; the
// requests carry no other device-specific field.
const hydraDeviceVerifyPath = "/oauth2/device/verify"

// errHydraDeviceFlowChallenge refuses a device-flow challenge at the generic
// login/consent endpoints (updatedData.code "device_flow_challenge"). The
// browser pages show their usual failure state for it.
var errHydraDeviceFlowChallenge = &oauth2CodedError{
	Status:  fiber.StatusForbidden,
	Code:    "device_flow_challenge",
	Message: "device flow challenges are not accepted at this endpoint",
}

// isDeviceFlowRequestURL reports whether a login or consent request belongs to
// a device flow, i.e. whether its request_url path ends with
// /oauth2/device/verify. A trailing slash is ignored, and a URL that does not
// parse is checked on its raw path so that it cannot slip past the check.
func isDeviceFlowRequestURL(requestURL string) bool {
	requestURL = strings.TrimSpace(requestURL)
	if requestURL == "" {
		return false
	}
	requestPath := requestURL
	if parsed, err := url.Parse(requestURL); err == nil {
		requestPath = parsed.Path
	} else if end := strings.IndexAny(requestPath, "?#"); end >= 0 {
		requestPath = requestPath[:end]
	}
	return strings.HasSuffix(strings.TrimRight(requestPath, "/"), hydraDeviceVerifyPath)
}

// deviceFlowChallengeError logs the refusal, since a device challenge never
// reaches a browser and one arriving here means it leaked. The challenge
// itself is a credential and is not logged.
func deviceFlowChallengeError(kind string, clientID string) error {
	harukiLogger.Warnf("Refused a device-flow %s challenge at a generic OAuth2 endpoint: client=%s", kind, strings.TrimSpace(clientID))
	return errHydraDeviceFlowChallenge
}

// getGenericHydraLoginRequest loads a login request for the generic
// /api/oauth2/login endpoints and refuses one that belongs to a device flow.
// Device challenges never reach a browser; only the device endpoints may
// decide them, so a leaked one cannot be accepted or rejected from here.
func getGenericHydraLoginRequest(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, challenge string) (*hydraLoginRequestResponse, error) {
	loginReq, err := getHydraLoginRequest(ctx, hydraConfig, challenge)
	if err != nil {
		return nil, err
	}
	if isDeviceFlowRequestURL(loginReq.RequestURL) {
		return nil, deviceFlowChallengeError("login", loginReq.Client.ClientID)
	}
	return loginReq, nil
}

// ensureHydraConsentNotDeviceFlow refuses a device-flow consent request at the
// generic consent endpoints (including the legacy
// /api/oauth2/authorize/consent), for the reason given at
// getGenericHydraLoginRequest. Callers run it after the subject check: a
// signed-in user holding someone else's challenge gets the usual subject
// mismatch and does not learn that the request belongs to a device flow.
func ensureHydraConsentNotDeviceFlow(consentReq *hydraConsentRequestResponse) error {
	if isDeviceFlowRequestURL(consentReq.RequestURL) {
		return deviceFlowChallengeError("consent", consentReq.Client.ClientID)
	}
	return nil
}
