package api

import (
	"bytes"
	"context"
	"fmt"
	platformIdentity "haruki-suite/internal/platform/identity"
	harukiLogger "haruki-suite/utils/logger"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

func (s *SessionHandler) VerifyKratosPassword(ctx context.Context, identifier string, password string) error {
	sessionToken, err := s.LoginWithKratosPassword(ctx, identifier, password)
	if err != nil {
		return err
	}
	if err := s.RevokeKratosSessionByToken(ctx, sessionToken); err != nil {
		harukiLogger.Warnf("Failed to revoke temporary Kratos verification session: %v", err)
		return fmt.Errorf("%w: revoke temporary verification session failed: %v", errIdentityProviderUnavailable, err)
	}
	return nil
}

func (s *SessionHandler) VerifyKratosPasswordByIdentityID(ctx context.Context, identityID string, password string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}

	identity, err := s.fetchKratosIdentityByID(ctx, identityID)
	if err != nil {
		return err
	}
	identifier := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(*identity))
	if identifier == "" {
		return fmt.Errorf("%w: identity email is empty", errKratosIdentityUnmapped)
	}
	return s.VerifyKratosPassword(ctx, identifier, password)
}

func (s *SessionHandler) UpdateKratosEmailByIdentityID(ctx context.Context, identityID string, email string) error {
	identityID = strings.TrimSpace(identityID)
	email = platformIdentity.NormalizeEmail(email)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if email == "" {
		return fmt.Errorf("%w: empty email", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	if err := s.updateKratosEmailViaPatch(ctx, identityID, email); err == nil {
		return nil
	} else if !shouldFallbackFromKratosPatch(err) {
		return err
	}
	if err := s.updateKratosEmailViaPut(ctx, identityID, email); err == nil {
		return nil
	} else {
		return err
	}
}

func (s *SessionHandler) ListKratosSessionsByIdentityID(ctx context.Context, identityID string) ([]KratosSessionInfo, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return nil, fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID)+"/sessions")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build list sessions request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: list sessions request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read list sessions response: %v", errIdentityProviderUnavailable, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var parsed []kratosAdminSessionRecord
		if err := sonic.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("%w: decode list sessions response: %v", errIdentityProviderUnavailable, err)
		}
		items := make([]KratosSessionInfo, 0, len(parsed))
		for _, row := range parsed {
			sessionID := strings.TrimSpace(row.ID)
			if sessionID == "" {
				continue
			}
			var expiresAt *time.Time
			if row.ExpiresAt != nil {
				expiresAtUTC := row.ExpiresAt.UTC()
				expiresAt = &expiresAtUTC
			}
			items = append(items, KratosSessionInfo{
				ID:        sessionID,
				Active:    row.Active,
				ExpiresAt: expiresAt,
			})
		}
		return items, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: list sessions failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("%w: list sessions failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (s *SessionHandler) ResolveKratosSessionID(ctx context.Context, sessionToken string, cookieHeader string) (string, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	cookieHeader = strings.TrimSpace(cookieHeader)
	if sessionToken == "" && cookieHeader == "" {
		return "", fmt.Errorf("%w: missing session token", errSessionUnauthorized)
	}

	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, cookieHeader)
	if err != nil {
		return "", err
	}
	if !whoami.Active {
		return "", fmt.Errorf("%w: kratos session is not active", errSessionUnauthorized)
	}

	sessionID := strings.TrimSpace(whoami.ID)
	if sessionID == "" {
		return "", fmt.Errorf("%w: missing kratos session id", errKratosInvalidInput)
	}
	return sessionID, nil
}

func (s *SessionHandler) FindKratosIdentityIDByEmail(ctx context.Context, email string) (string, error) {
	email = platformIdentity.NormalizeEmail(email)
	if email == "" {
		return "", fmt.Errorf("%w: empty email", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return "", fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	targetURL, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid admin identities url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("credentials_identifier", email)
	query.Set("page_size", "2")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("%w: build list identities request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: list identities request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read list identities response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return "", fmt.Errorf("%w: list identities failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return "", fmt.Errorf("%w: list identities failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var identities []kratosIdentityRecord
	if err := sonic.Unmarshal(body, &identities); err != nil {
		return "", fmt.Errorf("%w: decode list identities response: %v", errIdentityProviderUnavailable, err)
	}
	if len(identities) == 0 {
		return "", fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}

	for _, identity := range identities {
		identityEmail := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(identity))
		if identityEmail != email {
			continue
		}
		identityID := strings.TrimSpace(identity.ID)
		if identityID != "" {
			return identityID, nil
		}
	}
	return "", fmt.Errorf("%w: ambiguous or empty identity result for email", errKratosInvalidInput)
}

func (s *SessionHandler) UpdateKratosPasswordByIdentityID(ctx context.Context, identityID string, newPassword string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(newPassword) == "" {
		return fmt.Errorf("%w: empty password", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	if err := s.updateKratosPasswordViaPatch(ctx, identityID, newPassword); err == nil {
		return nil
	} else if !shouldFallbackFromKratosPatch(err) {
		return err
	}
	if err := s.updateKratosPasswordViaPut(ctx, identityID, newPassword); err == nil {
		return nil
	} else {
		return err
	}
}

func (s *SessionHandler) RevokeKratosSessionsByIdentityID(ctx context.Context, identityID string) error {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID)+"/sessions")
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build revoke sessions request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: revoke sessions request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read revoke sessions response: %v", errIdentityProviderUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: revoke sessions failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("%w: revoke sessions failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (s *SessionHandler) fetchKratosIdentityByID(ctx context.Context, identityID string) (*kratosIdentityRecord, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, fmt.Errorf("%w: empty identity id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return nil, fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build get identity request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: get identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read get identity response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: get identity failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("%w: get identity failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var identity kratosIdentityRecord
	if err := sonic.Unmarshal(body, &identity); err != nil {
		return nil, fmt.Errorf("%w: decode identity response: %v", errIdentityProviderUnavailable, err)
	}
	if strings.TrimSpace(identity.ID) == "" {
		identity.ID = identityID
	}
	return &identity, nil
}

func (s *SessionHandler) RevokeKratosSessionByToken(ctx context.Context, sessionToken string) error {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return fmt.Errorf("%w: empty session token", errKratosInvalidInput)
	}
	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, "")
	if err != nil {
		return err
	}
	sessionID := strings.TrimSpace(whoami.ID)
	if sessionID == "" {
		return fmt.Errorf("%w: missing kratos session id", errKratosInvalidInput)
	}
	return s.RevokeKratosSessionByID(ctx, sessionID)
}

func (s *SessionHandler) RevokeKratosSessionByID(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: empty session id", errKratosInvalidInput)
	}
	if strings.TrimSpace(s.KratosAdminURL) == "" {
		return fmt.Errorf("%w: kratos admin url is not configured", errIdentityProviderUnavailable)
	}

	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/sessions/"+url.PathEscape(sessionID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build revoke session request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: revoke session request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read revoke session response: %v", errIdentityProviderUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%w: session not found", errKratosSessionNotFound)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: revoke session failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("%w: revoke session failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (s *SessionHandler) updateKratosPasswordViaPatch(ctx context.Context, identityID string, newPassword string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	patchPayload := []map[string]any{
		{
			"op":    "replace",
			"path":  "/credentials/password/config/password",
			"value": newPassword,
		},
	}
	encoded, err := sonic.Marshal(patchPayload)
	if err != nil {
		return fmt.Errorf("%w: encode patch payload: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build patch request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: patch request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read patch response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	// PATCH may be unsupported by some Kratos versions/setups; caller may fall back to PUT.
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusUnsupportedMediaType || resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: patch strategy unsupported", errKratosInvalidInput)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: patch failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("%w: patch failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (s *SessionHandler) updateKratosEmailViaPatch(ctx context.Context, identityID string, email string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	patchPayload := []map[string]any{
		{
			"op":    "replace",
			"path":  "/traits/email",
			"value": email,
		},
	}
	encoded, err := sonic.Marshal(patchPayload)
	if err != nil {
		return fmt.Errorf("%w: encode patch payload: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build patch request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: patch request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read patch response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w: email already in use", errKratosIdentityConflict)
	}
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusUnsupportedMediaType || resp.StatusCode == http.StatusBadRequest {
		return fmt.Errorf("%w: patch strategy unsupported", errKratosInvalidInput)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: patch failed status=%d body=%s", errIdentityProviderUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("%w: patch failed status=%d body=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
}

func (s *SessionHandler) updateKratosPasswordViaPut(ctx context.Context, identityID string, newPassword string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build get identity request: %v", errIdentityProviderUnavailable, err)
	}
	getReq.Header.Set("Accept", "application/json")

	getResp, err := s.kratosHTTPClient().Do(getReq)
	if err != nil {
		return fmt.Errorf("%w: get identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(getResp.Body)
	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read get identity response: %v", errIdentityProviderUnavailable, err)
	}
	if getResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if getResp.StatusCode != http.StatusOK {
		if getResp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: get identity failed status=%d body=%s", errIdentityProviderUnavailable, getResp.StatusCode, strings.TrimSpace(string(getBody)))
		}
		return fmt.Errorf("%w: get identity failed status=%d body=%s", errKratosInvalidInput, getResp.StatusCode, strings.TrimSpace(string(getBody)))
	}

	var identity map[string]any
	if err := sonic.Unmarshal(getBody, &identity); err != nil {
		return fmt.Errorf("%w: decode identity response: %v", errIdentityProviderUnavailable, err)
	}
	applyPasswordIntoKratosIdentity(identity, newPassword)
	encoded, err := sonic.Marshal(identity)
	if err != nil {
		return fmt.Errorf("%w: encode identity update payload: %v", errIdentityProviderUnavailable, err)
	}

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build put identity request: %v", errIdentityProviderUnavailable, err)
	}
	putReq.Header.Set("Accept", "application/json")
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := s.kratosHTTPClient().Do(putReq)
	if err != nil {
		return fmt.Errorf("%w: put identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(putResp.Body)
	putBody, err := io.ReadAll(putResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read put identity response: %v", errIdentityProviderUnavailable, err)
	}
	if putResp.StatusCode == http.StatusOK || putResp.StatusCode == http.StatusNoContent {
		return nil
	}
	if putResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if putResp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: put identity failed status=%d body=%s", errIdentityProviderUnavailable, putResp.StatusCode, strings.TrimSpace(string(putBody)))
	}
	return fmt.Errorf("%w: put identity failed status=%d body=%s", errKratosInvalidInput, putResp.StatusCode, strings.TrimSpace(string(putBody)))
}

func (s *SessionHandler) updateKratosEmailViaPut(ctx context.Context, identityID string, email string) error {
	endpoint, err := buildProviderEndpoint(s.KratosAdminURL, "/admin/identities/"+url.PathEscape(identityID))
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("%w: build get identity request: %v", errIdentityProviderUnavailable, err)
	}
	getReq.Header.Set("Accept", "application/json")

	getResp, err := s.kratosHTTPClient().Do(getReq)
	if err != nil {
		return fmt.Errorf("%w: get identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(getResp.Body)
	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read get identity response: %v", errIdentityProviderUnavailable, err)
	}
	if getResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if getResp.StatusCode != http.StatusOK {
		if getResp.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: get identity failed status=%d body=%s", errIdentityProviderUnavailable, getResp.StatusCode, strings.TrimSpace(string(getBody)))
		}
		return fmt.Errorf("%w: get identity failed status=%d body=%s", errKratosInvalidInput, getResp.StatusCode, strings.TrimSpace(string(getBody)))
	}

	var identity map[string]any
	if err := sonic.Unmarshal(getBody, &identity); err != nil {
		return fmt.Errorf("%w: decode identity response: %v", errIdentityProviderUnavailable, err)
	}
	applyEmailIntoKratosIdentity(identity, email)
	encoded, err := sonic.Marshal(identity)
	if err != nil {
		return fmt.Errorf("%w: encode identity update payload: %v", errIdentityProviderUnavailable, err)
	}

	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build put identity request: %v", errIdentityProviderUnavailable, err)
	}
	putReq.Header.Set("Accept", "application/json")
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := s.kratosHTTPClient().Do(putReq)
	if err != nil {
		return fmt.Errorf("%w: put identity request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(putResp.Body)
	putBody, err := io.ReadAll(putResp.Body)
	if err != nil {
		return fmt.Errorf("%w: read put identity response: %v", errIdentityProviderUnavailable, err)
	}
	if putResp.StatusCode == http.StatusOK || putResp.StatusCode == http.StatusNoContent {
		return nil
	}
	if putResp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: identity not found", errKratosIdentityUnmapped)
	}
	if putResp.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w: email already in use", errKratosIdentityConflict)
	}
	if putResp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("%w: put identity failed status=%d body=%s", errIdentityProviderUnavailable, putResp.StatusCode, strings.TrimSpace(string(putBody)))
	}
	return fmt.Errorf("%w: put identity failed status=%d body=%s", errKratosInvalidInput, putResp.StatusCode, strings.TrimSpace(string(putBody)))
}

func applyPasswordIntoKratosIdentity(identity map[string]any, password string) {
	credentials, ok := identity["credentials"].(map[string]any)
	if !ok || credentials == nil {
		credentials = map[string]any{}
	}
	passwordCredentials, ok := credentials["password"].(map[string]any)
	if !ok || passwordCredentials == nil {
		passwordCredentials = map[string]any{}
	}
	passwordCredentials["config"] = map[string]any{
		"password": password,
	}
	credentials["password"] = passwordCredentials
	identity["credentials"] = credentials
}

func applyEmailIntoKratosIdentity(identity map[string]any, email string) {
	traits, ok := identity["traits"].(map[string]any)
	if !ok || traits == nil {
		traits = map[string]any{}
	}
	traits["email"] = email
	identity["traits"] = traits
}

func shouldFallbackFromKratosPatch(err error) bool {
	return IsKratosInvalidInputError(err)
}
