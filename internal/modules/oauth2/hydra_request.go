package oauth2

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	harukiOAuth2 "haruki-suite/utils/oauth2"

	"github.com/bytedance/sonic"
)

func getHydraLoginRequest(ctx context.Context, challenge string) (*hydraLoginRequestResponse, error) {
	response, err := sendHydraAdminRequest(ctx, http.MethodGet, "/admin/oauth2/auth/requests/login", url.Values{"login_challenge": {challenge}}, nil)
	if err != nil {
		return nil, err
	}
	var parsed hydraLoginRequestResponse
	if err := sonic.Unmarshal(response, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode hydra login request: %w", err)
	}
	return &parsed, nil
}

func getHydraConsentRequest(ctx context.Context, challenge string) (*hydraConsentRequestResponse, error) {
	response, err := sendHydraAdminRequest(ctx, http.MethodGet, "/admin/oauth2/auth/requests/consent", url.Values{"consent_challenge": {challenge}}, nil)
	if err != nil {
		return nil, err
	}
	var parsed hydraConsentRequestResponse
	if err := sonic.Unmarshal(response, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode hydra consent request: %w", err)
	}
	return &parsed, nil
}

func sendHydraAdminJSON(ctx context.Context, method string, endpointPath string, query url.Values, payload map[string]any) (*hydraRedirectResponse, error) {
	response, err := sendHydraAdminRequest(ctx, method, endpointPath, query, payload)
	if err != nil {
		return nil, err
	}
	var parsed hydraRedirectResponse
	if err := sonic.Unmarshal(response, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode hydra redirect response: %w", err)
	}
	return &parsed, nil
}

func sendHydraAdminRequest(ctx context.Context, method string, endpointPath string, query url.Values, payload map[string]any) ([]byte, error) {
	targetURL, err := harukiOAuth2.HydraAdminEndpoint(endpointPath)
	if err != nil {
		return nil, err
	}
	if encoded := query.Encode(); encoded != "" {
		targetURL += "?" + encoded
	}

	var requestBody []byte
	if payload != nil {
		requestBody, err = sonic.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode hydra request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create hydra request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if clientID, clientSecret := harukiOAuth2.HydraClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := hydraHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call hydra: %w", err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read hydra response: %w", err)
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
		return nil, &hydraRequestError{Status: resp.StatusCode, Message: message}
	}

	return body, nil
}

func hydraHTTPClient() *http.Client {
	timeout := harukiOAuth2.HydraRequestTimeout()
	timeoutNano := timeout.Nanoseconds()

	hydraHTTPClientMu.Lock()
	defer hydraHTTPClientMu.Unlock()

	if hydraSharedHTTPClient != nil && hydraSharedTimeoutNano == timeoutNano {
		return hydraSharedHTTPClient
	}

	client := &http.Client{Timeout: timeout}
	hydraSharedHTTPClient = client
	hydraSharedTimeoutNano = timeoutNano
	return hydraSharedHTTPClient
}
