package oauth2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	harukiOAuth2 "haruki-suite/utils/oauth2"
)

const (
	hydraClientMetadataNamespace = "haruki"
	hydraClientActiveKey         = "active"
)

type HydraOAuthClient struct {
	ClientID                string         `json:"client_id"`
	ClientSecret            string         `json:"client_secret,omitempty"`
	ClientName              string         `json:"client_name"`
	TokenEndpointAuthMethod string         `json:"token_endpoint_auth_method,omitempty"`
	RedirectURIs            []string       `json:"redirect_uris,omitempty"`
	GrantTypes              []string       `json:"grant_types,omitempty"`
	ResponseTypes           []string       `json:"response_types,omitempty"`
	Scope                   string         `json:"scope,omitempty"`
	Metadata                map[string]any `json:"metadata,omitempty"`
	CreatedAt               *time.Time     `json:"created_at,omitempty"`
}

type HydraOAuthClientUpsertInput struct {
	ClientID     string
	ClientSecret string
	ClientName   string
	ClientType   string
	RedirectURIs []string
	Scopes       []string
	Active       bool
}

func HydraRequestStatusCode(err error) int {
	var requestErr *hydraRequestError
	if errors.As(err, &requestErr) {
		return requestErr.Status
	}
	return 0
}

func IsHydraNotFoundError(err error) bool {
	return HydraRequestStatusCode(err) == http.StatusNotFound
}

func IsHydraConflictError(err error) bool {
	status := HydraRequestStatusCode(err)
	return status == http.StatusConflict || status == http.StatusBadRequest
}

func HydraOAuthClientScopes(client *HydraOAuthClient) []string {
	if client == nil {
		return nil
	}
	return strings.Fields(strings.TrimSpace(client.Scope))
}

func HydraOAuthClientActive(client *HydraOAuthClient) bool {
	if client == nil || client.Metadata == nil {
		return true
	}
	namespaceRaw, ok := client.Metadata[hydraClientMetadataNamespace]
	if !ok {
		return true
	}
	namespace, ok := namespaceRaw.(map[string]any)
	if !ok {
		return true
	}
	activeRaw, ok := namespace[hydraClientActiveKey]
	if !ok {
		return true
	}
	active, ok := activeRaw.(bool)
	if !ok {
		return true
	}
	return active
}

func ListHydraOAuthClients(ctx context.Context) ([]HydraOAuthClient, error) {
	pageToken := ""
	seenPageTokens := make(map[string]struct{})
	clients := make([]HydraOAuthClient, 0)
	for {
		page, nextPageToken, err := listHydraOAuthClientsPage(ctx, pageToken)
		if err != nil {
			return nil, err
		}
		clients = append(clients, page...)
		if nextPageToken == "" {
			return clients, nil
		}
		if _, exists := seenPageTokens[nextPageToken]; exists {
			return nil, fmt.Errorf("hydra oauth client pagination loop detected")
		}
		seenPageTokens[nextPageToken] = struct{}{}
		pageToken = nextPageToken
	}
}

func GetHydraOAuthClient(ctx context.Context, clientID string) (*HydraOAuthClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, fmt.Errorf("client id is required")
	}
	return sendHydraClientRequest(ctx, http.MethodGet, "/admin/clients/"+url.PathEscape(clientID), nil)
}

func CreateHydraOAuthClient(ctx context.Context, input HydraOAuthClientUpsertInput) (*HydraOAuthClient, error) {
	payload := buildHydraOAuthClientPayload(input)
	return sendHydraClientRequest(ctx, http.MethodPost, "/admin/clients", payload)
}

func UpdateHydraOAuthClient(ctx context.Context, clientID string, input HydraOAuthClientUpsertInput) (*HydraOAuthClient, error) {
	payload := buildHydraOAuthClientPayload(input)
	return sendHydraClientRequest(ctx, http.MethodPut, "/admin/clients/"+url.PathEscape(strings.TrimSpace(clientID)), payload)
}

func SetHydraOAuthClientActive(ctx context.Context, clientID string, active bool) (*HydraOAuthClient, error) {
	current, err := GetHydraOAuthClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	input := hydraOAuthClientToUpsertInput(current)
	input.Active = active
	return UpdateHydraOAuthClient(ctx, clientID, input)
}

func RotateHydraOAuthClientSecret(ctx context.Context, clientID string, newSecret string) (*HydraOAuthClient, error) {
	current, err := GetHydraOAuthClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	input := hydraOAuthClientToUpsertInput(current)
	input.ClientSecret = strings.TrimSpace(newSecret)
	return UpdateHydraOAuthClient(ctx, clientID, input)
}

func DeleteHydraOAuthClient(ctx context.Context, clientID string) error {
	_, err := sendHydraAdminRequest(ctx, http.MethodDelete, "/admin/clients/"+url.PathEscape(strings.TrimSpace(clientID)), nil, nil)
	return err
}

func DeleteHydraOAuthTokensByClientID(ctx context.Context, clientID string) error {
	query := url.Values{}
	query.Set("client_id", strings.TrimSpace(clientID))
	_, err := sendHydraAdminRequest(ctx, http.MethodDelete, "/admin/oauth2/tokens", query, nil)
	return err
}

func RevokeHydraConsentSessionsByClient(ctx context.Context, clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return fmt.Errorf("client id is required")
	}
	query := url.Values{}
	query.Set("client", clientID)
	query.Set("all", "true")
	_, err := sendHydraAdminRequest(ctx, http.MethodDelete, "/admin/oauth2/auth/sessions/consent", query, nil)
	return err
}

func listHydraOAuthClientsPage(ctx context.Context, pageToken string) ([]HydraOAuthClient, string, error) {
	targetURL, err := harukiOAuth2.HydraAdminEndpoint("/admin/clients")
	if err != nil {
		return nil, "", err
	}
	query := url.Values{}
	query.Set("page_size", "500")
	if trimmedPageToken := strings.TrimSpace(pageToken); trimmedPageToken != "" {
		query.Set("page_token", trimmedPageToken)
	}
	if encoded := query.Encode(); encoded != "" {
		targetURL += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create hydra oauth client list request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if clientID, clientSecret := harukiOAuth2.HydraClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := hydraHTTPClient().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to call hydra oauth client list: %w", err)
	}
	defer func(body io.ReadCloser) { _ = body.Close() }(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read hydra oauth client list response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", parseHydraRequestError(resp.StatusCode, body)
	}

	var clients []HydraOAuthClient
	if len(body) > 0 {
		if err := json.Unmarshal(body, &clients); err != nil {
			return nil, "", fmt.Errorf("failed to decode hydra oauth clients: %w", err)
		}
	}
	return clients, extractHydraNextPageToken(resp.Header.Values("Link")), nil
}

func sendHydraClientRequest(ctx context.Context, method string, endpointPath string, payload map[string]any) (*HydraOAuthClient, error) {
	responseBody, err := sendHydraClientRequestRaw(ctx, method, endpointPath, payload)
	if err != nil {
		return nil, err
	}
	var client HydraOAuthClient
	if err := json.Unmarshal(responseBody, &client); err != nil {
		return nil, fmt.Errorf("failed to decode hydra oauth client response: %w", err)
	}
	return &client, nil
}

func sendHydraClientRequestRaw(ctx context.Context, method string, endpointPath string, payload map[string]any) ([]byte, error) {
	targetURL, err := harukiOAuth2.HydraAdminEndpoint(endpointPath)
	if err != nil {
		return nil, err
	}
	var requestBody []byte
	if payload != nil {
		requestBody, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode hydra oauth client payload: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create hydra oauth client request: %w", err)
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
		return nil, fmt.Errorf("failed to call hydra oauth client endpoint: %w", err)
	}
	defer func(body io.ReadCloser) { _ = body.Close() }(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read hydra oauth client response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseHydraRequestError(resp.StatusCode, body)
	}
	return body, nil
}

func parseHydraRequestError(status int, body []byte) error {
	message := http.StatusText(status)
	var hydraErr hydraErrorResponse
	if err := json.Unmarshal(body, &hydraErr); err == nil {
		for _, candidate := range []string{hydraErr.ErrorDescription, hydraErr.Message, hydraErr.Error} {
			if strings.TrimSpace(candidate) != "" {
				message = candidate
				break
			}
		}
	}
	return &hydraRequestError{Status: status, Message: message}
}

func buildHydraOAuthClientPayload(input HydraOAuthClientUpsertInput) map[string]any {
	clientType := strings.TrimSpace(input.ClientType)
	if clientType == "" {
		clientType = oauthClientTypePublic
	}
	payload := map[string]any{
		"client_id":                  strings.TrimSpace(input.ClientID),
		"client_name":                strings.TrimSpace(input.ClientName),
		"redirect_uris":              append([]string(nil), input.RedirectURIs...),
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      strings.Join(input.Scopes, " "),
		"token_endpoint_auth_method": hydraAuthMethodFromClientType(clientType),
		"metadata":                   buildHydraClientMetadata(input.Active),
	}
	if method := strings.TrimSpace(payload["token_endpoint_auth_method"].(string)); method != "none" {
		payload["client_secret"] = strings.TrimSpace(input.ClientSecret)
	}
	return payload
}

func hydraAuthMethodFromClientType(clientType string) string {
	switch strings.TrimSpace(clientType) {
	case oauthClientTypeConfidential:
		return "client_secret_basic"
	default:
		return "none"
	}
}

func buildHydraClientMetadata(active bool) map[string]any {
	return map[string]any{
		hydraClientMetadataNamespace: map[string]any{
			hydraClientActiveKey: active,
		},
	}
}

func hydraOAuthClientToUpsertInput(client *HydraOAuthClient) HydraOAuthClientUpsertInput {
	if client == nil {
		return HydraOAuthClientUpsertInput{Active: true}
	}
	return HydraOAuthClientUpsertInput{
		ClientID:     client.ClientID,
		ClientName:   client.ClientName,
		ClientType:   HydraClientTypeFromAuthMethod(client.TokenEndpointAuthMethod),
		RedirectURIs: append([]string(nil), client.RedirectURIs...),
		Scopes:       HydraOAuthClientScopes(client),
		Active:       HydraOAuthClientActive(client),
	}
}
