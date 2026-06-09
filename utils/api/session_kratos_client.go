package api

import (
	"bytes"
	"context"
	"fmt"
	platformIdentity "haruki-suite/internal/platform/identity"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/bytedance/sonic"
)

func (s *SessionHandler) ResolveUserIDFromKratosSession(ctx context.Context, sessionToken string, cookieHeader string) (string, error) {
	resolved, err := s.resolveKratosSession(ctx, sessionToken, cookieHeader)
	if err != nil {
		return "", err
	}
	return resolved.UserID, nil
}

func (s *SessionHandler) LoginWithKratosPassword(ctx context.Context, identifier string, password string) (string, error) {
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/login/api")
	if err != nil {
		return "", err
	}

	payload := map[string]any{
		"method":     "password",
		"identifier": strings.TrimSpace(identifier),
		"password":   password,
	}
	return s.submitKratosSelfServiceFlow(ctx, "/self-service/login", flowID, payload)
}

func (s *SessionHandler) RegisterWithKratosPassword(ctx context.Context, email string, password string, extraTraits map[string]any) (string, error) {
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/registration/api")
	if err != nil {
		return "", err
	}

	traits := map[string]any{
		"email": platformIdentity.NormalizeEmail(email),
	}
	for k, v := range extraTraits {
		if strings.TrimSpace(k) == "" || k == "email" {
			continue
		}
		traits[k] = v
	}

	payload := map[string]any{
		"method":   "password",
		"password": password,
		"traits":   traits,
	}
	return s.submitKratosSelfServiceFlow(ctx, "/self-service/registration", flowID, payload)
}

func (s *SessionHandler) StartKratosRecoveryByEmail(ctx context.Context, email string) error {
	email = platformIdentity.NormalizeEmail(email)
	if email == "" {
		return fmt.Errorf("%w: empty email", errKratosInvalidInput)
	}
	return s.startKratosRecoveryByEmailWithMethod(ctx, email, "code")
}

func (s *SessionHandler) startKratosRecoveryByEmailWithMethod(ctx context.Context, email string, method string) error {
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/recovery/api")
	if err != nil {
		return err
	}
	return s.submitKratosRecoveryFlow(ctx, flowID, method, email)
}

func (s *SessionHandler) ResetKratosPasswordByRecoveryCode(ctx context.Context, recoveryCode string, newPassword string) (string, string, error) {
	recoveryCode = strings.TrimSpace(recoveryCode)
	if recoveryCode == "" {
		return "", "", fmt.Errorf("%w: empty recovery code", errKratosInvalidInput)
	}
	if strings.TrimSpace(newPassword) == "" {
		return "", "", fmt.Errorf("%w: empty password", errKratosInvalidInput)
	}

	sessionToken, err := s.verifyKratosRecoveryCode(ctx, recoveryCode)
	if err != nil {
		return "", "", err
	}

	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, "")
	if err != nil {
		return "", "", err
	}
	if !whoami.Active {
		return "", "", fmt.Errorf("%w: kratos session is not active", errSessionUnauthorized)
	}
	identityID := strings.TrimSpace(whoami.Identity.ID)
	if identityID == "" {
		return "", "", fmt.Errorf("%w: empty identity id", errKratosIdentityUnmapped)
	}
	email := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(whoami.Identity))
	userID, err := s.resolveKratosIdentity(ctx, identityID, email)
	if err != nil {
		return "", "", err
	}

	if err := s.UpdateKratosPasswordByIdentityID(ctx, identityID, newPassword); err != nil {
		return "", "", err
	}
	return userID, identityID, nil
}

func (s *SessionHandler) resolveKratosSession(ctx context.Context, sessionToken string, cookieHeader string) (*resolvedKratosSession, error) {
	if !s.hasKratosProviderConfigured() {
		return nil, fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	sessionToken = strings.TrimSpace(sessionToken)
	cookieHeader = strings.TrimSpace(cookieHeader)
	if sessionToken == "" && cookieHeader == "" {
		return nil, fmt.Errorf("%w: missing session token", errSessionUnauthorized)
	}

	whoami, err := s.fetchKratosWhoami(ctx, sessionToken, cookieHeader)
	if err != nil {
		return nil, err
	}
	if !whoami.Active {
		return nil, fmt.Errorf("%w: kratos session is not active", errSessionUnauthorized)
	}
	identityID := strings.TrimSpace(whoami.Identity.ID)
	if identityID == "" {
		return nil, fmt.Errorf("%w: kratos identity id is empty", errSessionUnauthorized)
	}
	displayName := strings.TrimSpace(extractKratosIdentityName(whoami.Identity))
	var displayNamePtr *string
	if displayName != "" {
		displayNamePtr = &displayName
	}
	email := platformIdentity.NormalizeEmail(extractKratosIdentityEmail(whoami.Identity))
	emailVerified := extractKratosIdentityEmailVerification(whoami.Identity)
	userID, err := s.resolveKratosIdentity(ctx, identityID, email)
	if err != nil {
		return nil, err
	}
	s.syncResolvedUserProfile(ctx, userID, identityID, email, displayNamePtr)
	return &resolvedKratosSession{
		UserID:        userID,
		IdentityID:    identityID,
		DisplayName:   displayNamePtr,
		EmailVerified: emailVerified,
	}, nil
}

func (s *SessionHandler) fetchKratosWhoami(ctx context.Context, sessionToken string, cookieHeader string) (*kratosSessionWhoamiResponse, error) {
	whoamiURL, err := buildProviderEndpoint(s.KratosPublicURL, "/sessions/whoami")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, whoamiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if sessionToken != "" {
		req.Header.Set(s.KratosSessionHeader, sessionToken)
	}
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request whoami: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read whoami response: %v", errIdentityProviderUnavailable, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var parsed kratosSessionWhoamiResponse
		if err := sonic.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("%w: decode whoami payload: %v", errIdentityProviderUnavailable, err)
		}
		return &parsed, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: status %d", errSessionUnauthorized, resp.StatusCode)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: whoami endpoint returned 404", errIdentityProviderUnavailable)
	default:
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: status %d", errIdentityProviderUnavailable, resp.StatusCode)
		}
		return nil, fmt.Errorf("%w: status %d", errSessionUnauthorized, resp.StatusCode)
	}
}

func (s *SessionHandler) initKratosSelfServiceFlow(ctx context.Context, initPath string) (string, error) {
	if !s.hasKratosProviderConfigured() {
		return "", fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, initPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: build request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: init flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read init flow response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyKratosFlowError(resp.StatusCode, body)
	}

	var parsed kratosFlowResponse
	if err := sonic.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: decode init flow response: %v", errIdentityProviderUnavailable, err)
	}
	flowID := strings.TrimSpace(parsed.ID)
	if flowID == "" {
		return "", fmt.Errorf("%w: empty flow id", errIdentityProviderUnavailable)
	}
	return flowID, nil
}

func (s *SessionHandler) submitKratosSelfServiceFlow(ctx context.Context, submitPath string, flowID string, payload map[string]any) (string, error) {
	if !s.hasKratosProviderConfigured() {
		return "", fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, submitPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}

	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid submit url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("flow", strings.TrimSpace(flowID))
	endpoint.RawQuery = query.Encode()

	encoded, err := sonic.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("%w: encode payload: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: build submit request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: submit flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read submit response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyKratosFlowError(resp.StatusCode, body)
	}

	var parsed kratosAuthSubmitResponse
	if err := sonic.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: decode submit response: %v", errIdentityProviderUnavailable, err)
	}
	sessionToken := strings.TrimSpace(parsed.SessionToken)
	if sessionToken == "" {
		return "", fmt.Errorf("%w: empty session token in response", errIdentityProviderUnavailable)
	}
	return sessionToken, nil
}

func (s *SessionHandler) submitKratosRecoveryFlow(ctx context.Context, flowID string, method string, email string) error {
	if !s.hasKratosProviderConfigured() {
		return fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, "/self-service/recovery")
	if err != nil {
		return fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("%w: invalid submit url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("flow", strings.TrimSpace(flowID))
	endpoint.RawQuery = query.Encode()

	encoded, err := sonic.Marshal(map[string]any{
		"method": method,
		"email":  email,
	})
	if err != nil {
		return fmt.Errorf("%w: encode payload: %v", errIdentityProviderUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: build submit request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: submit flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: read submit response: %v", errIdentityProviderUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusSeeOther:
		return nil
	case http.StatusGone:
		return fmt.Errorf("%w: status=%d reason=%s", errKratosInvalidInput, resp.StatusCode, strings.TrimSpace(string(body)))
	default:
		return classifyKratosFlowError(resp.StatusCode, body)
	}
}

func (s *SessionHandler) verifyKratosRecoveryCode(ctx context.Context, recoveryCode string) (string, error) {
	if !s.hasKratosProviderConfigured() {
		return "", fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable)
	}
	flowID, err := s.initKratosSelfServiceFlow(ctx, "/self-service/recovery/api")
	if err != nil {
		return "", err
	}

	targetURL, err := buildProviderEndpoint(s.KratosPublicURL, "/self-service/recovery")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errIdentityProviderUnavailable, err)
	}
	endpoint, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid submit url: %v", errIdentityProviderUnavailable, err)
	}
	query := endpoint.Query()
	query.Set("flow", strings.TrimSpace(flowID))
	endpoint.RawQuery = query.Encode()

	encoded, err := sonic.Marshal(map[string]any{
		"method": "code",
		"code":   strings.TrimSpace(recoveryCode),
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode payload: %v", errIdentityProviderUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: build submit request: %v", errIdentityProviderUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.kratosHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: submit flow request failed: %v", errIdentityProviderUnavailable, err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read submit response: %v", errIdentityProviderUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", classifyKratosFlowError(resp.StatusCode, body)
	}

	var parsed kratosRecoveryFlowSubmitResponse
	if err := sonic.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: decode recovery response: %v", errIdentityProviderUnavailable, err)
	}
	sessionToken := strings.TrimSpace(extractSessionTokenFromRecoveryPayload(body, parsed))
	if sessionToken == "" {
		return "", fmt.Errorf("%w: missing session token after recovery (state=%s)", errKratosInvalidInput, strings.TrimSpace(parsed.State))
	}
	return sessionToken, nil
}

func classifyKratosFlowError(statusCode int, body []byte) error {
	bodyText := strings.TrimSpace(string(body))
	var payload kratosErrorPayload
	_ = sonic.Unmarshal(body, &payload)
	reason := strings.TrimSpace(extractKratosErrorReason(payload, bodyText))
	reasonLower := strings.ToLower(reason)

	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", errKratosInvalidCredentials, reason)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", errKratosIdentityConflict, reason)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		if strings.Contains(reasonLower, "already exists") ||
			strings.Contains(reasonLower, "exists already") ||
			strings.Contains(reasonLower, "already been registered") {
			return fmt.Errorf("%w: %s", errKratosIdentityConflict, reason)
		}
		if strings.Contains(reasonLower, "identifier") || strings.Contains(reasonLower, "password") || strings.Contains(reasonLower, "trait") {
			return fmt.Errorf("%w: %s", errKratosInvalidInput, reason)
		}
		return fmt.Errorf("%w: %s", errKratosInvalidCredentials, reason)
	default:
		if statusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: status=%d reason=%s", errIdentityProviderUnavailable, statusCode, reason)
		}
		return fmt.Errorf("%w: status=%d reason=%s", errKratosInvalidInput, statusCode, reason)
	}
}

func extractKratosErrorReason(payload kratosErrorPayload, fallback string) string {
	if payload.Error != nil {
		if text := strings.TrimSpace(payload.Error.Reason); text != "" {
			return text
		}
		if text := strings.TrimSpace(payload.Error.Text); text != "" {
			return text
		}
	}
	if payload.UI != nil {
		for _, item := range payload.UI.Messages {
			if text := strings.TrimSpace(item.Text); text != "" {
				return text
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return "kratos flow request failed"
}

func extractSessionTokenFromContinueWith(items []kratosContinueWithItem) string {
	for _, item := range items {
		action := strings.TrimSpace(item.Action)
		if action != "" && action != "set_ory_session_token" {
			continue
		}
		if token := strings.TrimSpace(item.OrySessionToken); token != "" {
			return token
		}
	}
	return ""
}

func extractSessionTokenFromRecoveryPayload(rawBody []byte, parsed kratosRecoveryFlowSubmitResponse) string {
	if token := strings.TrimSpace(extractSessionTokenFromContinueWith(parsed.ContinueWith)); token != "" {
		return token
	}

	var raw any
	if err := sonic.Unmarshal(rawBody, &raw); err != nil {
		return ""
	}
	return extractSessionTokenFromAny(raw)
}

func extractSessionTokenFromAny(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if token := trimAnyString(typed["ory_session_token"]); token != "" {
			return token
		}
		if token := trimAnyString(typed["session_token"]); token != "" {
			return token
		}

		action := strings.TrimSpace(trimAnyString(typed["action"]))
		if action == "set_ory_session_token" {
			if token := trimAnyString(typed["token"]); token != "" {
				return token
			}
			if nested, ok := typed["set_ory_session_token"]; ok {
				if token := extractSessionTokenFromAny(nested); token != "" {
					return token
				}
			}
		}

		for _, child := range typed {
			if token := extractSessionTokenFromAny(child); token != "" {
				return token
			}
		}
	case []any:
		for _, child := range typed {
			if token := extractSessionTokenFromAny(child); token != "" {
				return token
			}
		}
	}
	return ""
}

func trimAnyString(value any) string {
	raw, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func buildProviderEndpoint(baseURL, endpointPath string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("provider base URL is not configured")
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid provider base URL: %w", err)
	}
	if parsedBase.Scheme == "" || parsedBase.Host == "" {
		return "", fmt.Errorf("invalid provider base URL")
	}

	cleanPath := endpointPath
	if cleanPath == "" {
		cleanPath = "/"
	}
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}

	basePath := strings.TrimRight(parsedBase.EscapedPath(), "/")
	if basePath == "" {
		parsedBase.Path = cleanPath
		return parsedBase.String(), nil
	}

	joined := path.Clean(strings.TrimRight(basePath, "/") + cleanPath)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	parsedBase.Path = joined
	return parsedBase.String(), nil
}
