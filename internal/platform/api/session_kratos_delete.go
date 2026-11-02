package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DeleteKratosIdentity removes the login identity and its sessions. A missing
// identity is success so an interrupted account deletion can be retried.
func (s *SessionHandler) DeleteKratosIdentity(ctx context.Context, identityID string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if s == nil || strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: invalid kratos admin url", errIdentityProviderUnavailable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build delete identity request", errIdentityProviderUnavailable)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: delete identity request failed", errIdentityProviderUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		// Provider bodies may contain identity data; keep them out of errors/logs.
		return fmt.Errorf("%w: delete identity status=%d", errIdentityProviderUnavailable, resp.StatusCode)
	}
}
