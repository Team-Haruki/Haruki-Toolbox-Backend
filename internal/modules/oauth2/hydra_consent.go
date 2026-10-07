package oauth2

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"

	"encoding/json/jsontext"
	json "encoding/json/v2"
)

type HydraConsentClient struct {
	ClientID                string `json:"client_id"`
	ClientName              string `json:"client_name"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
}

type HydraConsentRequest struct {
	Client HydraConsentClient `json:"client"`
	// RequestURL is the authorization URL that started the flow; a device
	// flow's is Hydra's /oauth2/device/verify.
	RequestURL string `json:"request_url"`
}

type HydraConsentSession struct {
	ConsentRequestID string              `json:"consent_request_id"`
	GrantScope       []string            `json:"grant_scope"`
	HandledAt        *time.Time          `json:"handled_at"`
	ConsentRequest   HydraConsentRequest `json:"consent_request"`
	// Context is the context of the consent accept. Any JSON value decodes;
	// FlowType and DeviceLabel read it leniently, so an unexpected shape never
	// fails the session list.
	Context jsontext.Value `json:"context"`
}

// Flow types of a consent session, as the authorization lists report them.
const (
	HydraConsentFlowTypeBrowser = "browser"
	HydraConsentFlowTypeDevice  = "device"
)

// hydraConsentHarukiContext is the context.haruki block a device approval
// writes (buildHydraConsentAcceptBody). Members stay raw so that a member of
// an unexpected type is ignored instead of failing the decode.
type hydraConsentHarukiContext struct {
	Flow  jsontext.Value `json:"flow"`
	Label jsontext.Value `json:"label"`
}

// harukiContext returns context.haruki, or false when the context is missing,
// not an object, or has no object-valued haruki member.
func (s HydraConsentSession) harukiContext() (hydraConsentHarukiContext, bool) {
	var outer struct {
		Haruki jsontext.Value `json:"haruki"`
	}
	if len(s.Context) == 0 || s.Context.Kind() != '{' || json.Unmarshal(s.Context, &outer) != nil {
		return hydraConsentHarukiContext{}, false
	}
	if len(outer.Haruki) == 0 || outer.Haruki.Kind() != '{' {
		return hydraConsentHarukiContext{}, false
	}
	var haruki hydraConsentHarukiContext
	if json.Unmarshal(outer.Haruki, &haruki) != nil {
		return hydraConsentHarukiContext{}, false
	}
	return haruki, true
}

// jsonStringValue returns value when it is a JSON string, and false otherwise.
func jsonStringValue(value jsontext.Value) (string, bool) {
	if len(value) == 0 || value.Kind() != '"' {
		return "", false
	}
	var decoded string
	if json.Unmarshal(value, &decoded) != nil {
		return "", false
	}
	return decoded, true
}

// FlowType is "device" when the consent was granted through the device
// flow, i.e. context.haruki.flow is "device" or the consent request's
// request_url path ends with /oauth2/device/verify, and "browser" otherwise.
func (s HydraConsentSession) FlowType() string {
	if haruki, ok := s.harukiContext(); ok {
		if flow, ok := jsonStringValue(haruki.Flow); ok && flow == HydraConsentFlowTypeDevice {
			return HydraConsentFlowTypeDevice
		}
	}
	if isDeviceFlowRequestURL(s.ConsentRequest.RequestURL) {
		return HydraConsentFlowTypeDevice
	}
	return HydraConsentFlowTypeBrowser
}

// DeviceLabel is the label a device approval recorded in
// context.haruki.label, cleaned as on the way in, and "" for a browser
// authorization or a label that is missing or not a string.
func (s HydraConsentSession) DeviceLabel() string {
	if s.FlowType() != HydraConsentFlowTypeDevice {
		return ""
	}
	haruki, ok := s.harukiContext()
	if !ok {
		return ""
	}
	label, _ := jsonStringValue(haruki.Label)
	return sanitizeDeviceLabel(label)
}

func hydraConsentSessionKey(session HydraConsentSession) string {
	return strings.TrimSpace(session.ConsentRequestID) + "\x00" + strings.TrimSpace(session.ConsentRequest.Client.ClientID)
}

func HydraOAuthManagementEnabled(hydraConfig *harukiOAuth2.HydraConfig) bool {
	return hydraConfig != nil && hydraConfig.Enabled()
}

func ListHydraConsentSessions(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, subject string) ([]HydraConsentSession, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, fmt.Errorf("subject is required")
	}

	pageToken := ""
	seenPageTokens := make(map[string]struct{})
	sessions := make([]HydraConsentSession, 0)
	for {
		page, nextPageToken, err := listHydraConsentSessionsPage(ctx, hydraConfig, subject, pageToken)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, page...)
		if nextPageToken == "" {
			return sessions, nil
		}
		if _, exists := seenPageTokens[nextPageToken]; exists {
			return nil, fmt.Errorf("hydra consent session pagination loop detected")
		}
		seenPageTokens[nextPageToken] = struct{}{}
		pageToken = nextPageToken
	}
}

func ListHydraConsentSessionsForSubjects(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, subjects []string) ([]HydraConsentSession, error) {
	normalizedSubjects := normalizeHydraSubjects(subjects...)
	if len(normalizedSubjects) == 0 {
		return nil, fmt.Errorf("at least one subject is required")
	}

	sessions := make([]HydraConsentSession, 0)
	seen := make(map[string]struct{})
	for _, subject := range normalizedSubjects {
		items, err := ListHydraConsentSessions(ctx, hydraConfig, subject)
		if err != nil {
			return nil, err
		}
		for _, session := range items {
			key := hydraConsentSessionKey(session)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func listHydraConsentSessionsPage(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, subject, pageToken string) ([]HydraConsentSession, string, error) {
	targetURL, err := hydraConfig.AdminEndpoint("/admin/oauth2/auth/sessions/consent")
	if err != nil {
		return nil, "", err
	}
	query := url.Values{}
	query.Set("subject", subject)
	query.Set("page_size", "500")
	if trimmedPageToken := strings.TrimSpace(pageToken); trimmedPageToken != "" {
		query.Set("page_token", trimmedPageToken)
	}
	if encoded := query.Encode(); encoded != "" {
		targetURL += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create hydra request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if clientID, clientSecret := hydraConfig.ClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := hydraConfig.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to call hydra: %w", err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read hydra response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := http.StatusText(resp.StatusCode)
		var hydraErr hydraErrorResponse
		if err := json.Unmarshal(body, &hydraErr); err == nil {
			for _, candidate := range []string{hydraErr.ErrorDescription, hydraErr.Message, hydraErr.Error} {
				if strings.TrimSpace(candidate) != "" {
					message = candidate
					break
				}
			}
		}
		return nil, "", &hydraRequestError{Status: resp.StatusCode, Message: message}
	}

	var sessions []HydraConsentSession
	if len(body) > 0 {
		if err := json.Unmarshal(body, &sessions); err != nil {
			return nil, "", fmt.Errorf("failed to decode hydra consent sessions: %w", err)
		}
	}
	return sessions, extractHydraNextPageToken(resp.Header.Values("Link")), nil
}

func extractHydraNextPageToken(linkHeaders []string) string {
	for _, headerValue := range linkHeaders {
		for _, segment := range strings.Split(headerValue, ",") {
			segment = strings.TrimSpace(segment)
			if !strings.Contains(segment, `rel="next"`) {
				continue
			}
			start := strings.Index(segment, "<")
			end := strings.Index(segment, ">")
			if start < 0 || end <= start+1 {
				continue
			}
			nextURL, err := url.Parse(strings.TrimSpace(segment[start+1 : end]))
			if err != nil {
				continue
			}
			if token := strings.TrimSpace(nextURL.Query().Get("page_token")); token != "" {
				return token
			}
		}
	}
	return ""
}

// RevokeHydraConsentSessions sends DELETE /admin/oauth2/auth/sessions/consent with
// subject+client, or subject+all=true when clientID is empty. The query goes through
// url.Values, so a "+" in the subject is sent as %2B: Hydra reads a raw "+" as a
// space and then answers 204 without revoking anything.
func RevokeHydraConsentSessions(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, subject, clientID string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("subject is required")
	}
	query := url.Values{}
	query.Set("subject", subject)
	if clientID != "" {
		query.Set("client", strings.TrimSpace(clientID))
	} else {
		query.Set("all", "true")
	}
	_, err := sendHydraAdminRequest(ctx, hydraConfig, http.MethodDelete, "/admin/oauth2/auth/sessions/consent", query, nil)
	return err
}

// RevokeHydraConsentSessionByID revokes the one consent session (and the access
// and refresh tokens issued under it, refreshed ones included) created by a
// consent request: DELETE /admin/oauth2/auth/sessions/consent with only
// consent_request_id, the only parameter combination Hydra accepts for it. A
// device code still waiting to be redeemed is deleted with it (ON DELETE
// CASCADE). Hydra answers 204 for an unknown ID as well, so callers that act on
// a user's behalf must check ownership first.
func RevokeHydraConsentSessionByID(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, consentRequestID string) error {
	consentRequestID = strings.TrimSpace(consentRequestID)
	if consentRequestID == "" {
		return fmt.Errorf("consent request id is required")
	}
	query := url.Values{"consent_request_id": {consentRequestID}}
	_, err := sendHydraAdminRequest(ctx, hydraConfig, http.MethodDelete, "/admin/oauth2/auth/sessions/consent", query, nil)
	return err
}

// RevokeHydraConsentSessionsForSubjects revokes consent sessions one subject at a
// time: subject+client when clientID is set, subject+all=true (every client of the
// subject) when it is empty. Hydra v25.4.0 rejects client without subject, with or
// without all=true, so revoking a whole client means calling this with each of its
// subjects. Revoking a consent session also invalidates the access and refresh
// tokens issued under it.
//
// Every subject is tried. revoked counts the subjects Hydra accepted, failed lists
// the others, and err is non-nil when any subject failed or none was given.
func RevokeHydraConsentSessionsForSubjects(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string, subjects []string) (revoked int, failed []string, err error) {
	normalizedSubjects := normalizeHydraSubjects(subjects...)
	if len(normalizedSubjects) == 0 {
		return 0, nil, fmt.Errorf("at least one subject is required")
	}

	var firstErr error
	for _, subject := range normalizedSubjects {
		if revokeErr := RevokeHydraConsentSessions(ctx, hydraConfig, subject, clientID); revokeErr != nil {
			failed = append(failed, subject)
			if firstErr == nil {
				firstErr = revokeErr
			}
			continue
		}
		revoked++
	}
	if firstErr != nil {
		return revoked, failed, fmt.Errorf("failed to revoke hydra consent sessions for %d of %d subjects: %w", len(failed), len(normalizedSubjects), firstErr)
	}
	return revoked, nil, nil
}

func HydraConsentSessionExistsForClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, subject, clientID string) (bool, error) {
	subject = strings.TrimSpace(subject)
	clientID = strings.TrimSpace(clientID)
	if subject == "" || clientID == "" {
		return false, fmt.Errorf("subject and clientID are required")
	}
	sessions, err := ListHydraConsentSessions(ctx, hydraConfig, subject)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.ConsentRequest.Client.ClientID) == clientID {
			return true, nil
		}
	}
	return false, nil
}

func HydraConsentSessionExistsForSubjects(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, subjects []string, clientID string) (bool, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false, fmt.Errorf("clientID is required")
	}

	sessions, err := ListHydraConsentSessionsForSubjects(ctx, hydraConfig, subjects)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.ConsentRequest.Client.ClientID) == clientID {
			return true, nil
		}
	}
	return false, nil
}

func HydraClientTypeFromAuthMethod(method string) string {
	switch strings.TrimSpace(method) {
	case "", "none":
		return oauthClientTypePublic
	default:
		return oauthClientTypeConfidential
	}
}
