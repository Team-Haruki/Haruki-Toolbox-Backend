package oauth2

import (
	"context"
	"fmt"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

type HydraConsentClient struct {
	ClientID                string `json:"client_id"`
	ClientName              string `json:"client_name"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
}

type HydraConsentRequest struct {
	Client HydraConsentClient `json:"client"`
}

type HydraConsentSession struct {
	ConsentRequestID string              `json:"consent_request_id"`
	GrantScope       []string            `json:"grant_scope"`
	HandledAt        *time.Time          `json:"handled_at"`
	ConsentRequest   HydraConsentRequest `json:"consent_request"`
}

func hydraConsentSessionKey(session HydraConsentSession) string {
	return strings.TrimSpace(session.ConsentRequestID) + "\x00" + strings.TrimSpace(session.ConsentRequest.Client.ClientID)
}

func HydraOAuthManagementEnabled() bool {
	return harukiOAuth2.UseHydraProvider()
}

func ListHydraConsentSessions(ctx context.Context, subject string) ([]HydraConsentSession, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, fmt.Errorf("subject is required")
	}

	pageToken := ""
	seenPageTokens := make(map[string]struct{})
	sessions := make([]HydraConsentSession, 0)
	for {
		page, nextPageToken, err := listHydraConsentSessionsPage(ctx, subject, pageToken)
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

func ListHydraConsentSessionsForSubjects(ctx context.Context, subjects []string) ([]HydraConsentSession, error) {
	normalizedSubjects := normalizeHydraSubjects(subjects...)
	if len(normalizedSubjects) == 0 {
		return nil, fmt.Errorf("at least one subject is required")
	}

	sessions := make([]HydraConsentSession, 0)
	seen := make(map[string]struct{})
	for _, subject := range normalizedSubjects {
		items, err := ListHydraConsentSessions(ctx, subject)
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

func listHydraConsentSessionsPage(ctx context.Context, subject, pageToken string) ([]HydraConsentSession, string, error) {
	targetURL, err := harukiOAuth2.HydraAdminEndpoint("/admin/oauth2/auth/sessions/consent")
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
	if clientID, clientSecret := harukiOAuth2.HydraClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := hydraHTTPClient().Do(req)
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
		if err := sonic.Unmarshal(body, &hydraErr); err == nil {
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
		if err := sonic.Unmarshal(body, &sessions); err != nil {
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

func RevokeHydraConsentSessions(ctx context.Context, subject, clientID string) error {
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
	_, err := sendHydraAdminRequest(ctx, http.MethodDelete, "/admin/oauth2/auth/sessions/consent", query, nil)
	return err
}

func RevokeHydraConsentSessionsForSubjects(ctx context.Context, subjects []string, clientID string) error {
	normalizedSubjects := normalizeHydraSubjects(subjects...)
	if len(normalizedSubjects) == 0 {
		return fmt.Errorf("at least one subject is required")
	}

	var firstErr error
	for _, subject := range normalizedSubjects {
		if err := RevokeHydraConsentSessions(ctx, subject, clientID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func HydraConsentSessionExistsForClient(ctx context.Context, subject, clientID string) (bool, error) {
	subject = strings.TrimSpace(subject)
	clientID = strings.TrimSpace(clientID)
	if subject == "" || clientID == "" {
		return false, fmt.Errorf("subject and clientID are required")
	}
	sessions, err := ListHydraConsentSessions(ctx, subject)
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

func HydraConsentSessionExistsForSubjects(ctx context.Context, subjects []string, clientID string) (bool, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false, fmt.Errorf("clientID is required")
	}

	sessions, err := ListHydraConsentSessionsForSubjects(ctx, subjects)
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
