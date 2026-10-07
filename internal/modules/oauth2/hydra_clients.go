package oauth2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	json "encoding/json/v2"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

const (
	hydraClientMetadataNamespace = "haruki"
	hydraClientActiveKey         = "active"

	hydraGrantTypeAuthorizationCode  = "authorization_code"
	hydraGrantTypeRefreshToken       = "refresh_token"
	hydraResponseTypeCode            = "code"
	hydraAuthMethodNone              = "none"
	hydraAuthMethodClientSecretBasic = "client_secret_basic"

	hydraJSONPatchOpAdd     = "add"
	hydraJSONPatchOpReplace = "replace"
)

var (
	// ErrHydraPublicClientHasNoSecret: a public client authenticates with "none",
	// so a secret written for it would never be checked.
	ErrHydraPublicClientHasNoSecret = errors.New("public oauth client has no client secret")
	// ErrHydraClientSecretRequired: Hydra accepts a switch to a confidential auth
	// method without a secret, and the client can then never authenticate.
	ErrHydraClientSecretRequired = errors.New("oauth client secret is required")
	// ErrHydraPostLogoutRequiresRedirectURIs: Hydra rejects post_logout_redirect_uris
	// on a client without redirect_uris (invalid_client_metadata).
	ErrHydraPostLogoutRequiresRedirectURIs = errors.New("post logout redirect uris require redirect uris")
)

type HydraOAuthClient struct {
	ClientID                string         `json:"client_id"`
	ClientSecret            string         `json:"client_secret,omitempty"`
	ClientName              string         `json:"client_name"`
	TokenEndpointAuthMethod string         `json:"token_endpoint_auth_method,omitempty"`
	RedirectURIs            []string       `json:"redirect_uris,omitempty"`
	PostLogoutRedirectURIs  []string       `json:"post_logout_redirect_uris,omitempty"`
	GrantTypes              []string       `json:"grant_types,omitempty"`
	ResponseTypes           []string       `json:"response_types,omitempty"`
	Scope                   string         `json:"scope,omitempty"`
	Metadata                map[string]any `json:"metadata,omitempty"`
	CreatedAt               *time.Time     `json:"created_at,omitzero"`
}

type HydraOAuthClientUpsertInput struct {
	ClientID     string
	ClientSecret string
	ClientName   string
	ClientType   string
	RedirectURIs []string
	// PostLogoutRedirectURIs is written on create when non-empty. On update nil
	// keeps the registered list and a non-nil slice (empty included) replaces it.
	PostLogoutRedirectURIs []string
	Scopes                 []string
	// GrantTypes on create defaults to authorization_code + refresh_token when
	// empty. On update nil keeps grant_types and response_types as registered.
	GrantTypes []string
	// Active only seeds metadata on create; SetHydraOAuthClientActive changes it.
	Active bool
}

// HydraJSONPatchOp is one RFC 6902 operation for PATCH /admin/clients/{id}.
// Only "add" and "replace" are sent: Hydra answers "test" with 500 even when
// the value matches.
type HydraJSONPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitzero"`
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

func ListHydraOAuthClients(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig) ([]HydraOAuthClient, error) {
	pageToken := ""
	seenPageTokens := make(map[string]struct{})
	clients := make([]HydraOAuthClient, 0)
	for {
		page, nextPageToken, err := listHydraOAuthClientsPage(ctx, hydraConfig, pageToken)
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

func GetHydraOAuthClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string) (*HydraOAuthClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, fmt.Errorf("client id is required")
	}
	return sendHydraClientRequest(ctx, hydraConfig, http.MethodGet, "/admin/clients/"+url.PathEscape(clientID), nil)
}

func CreateHydraOAuthClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, input HydraOAuthClientUpsertInput) (*HydraOAuthClient, error) {
	payload := buildHydraOAuthClientPayload(input)
	return sendHydraClientRequest(ctx, hydraConfig, http.MethodPost, "/admin/clients", payload)
}

// UpdateHydraOAuthClient patches only the members the admin form owns, so grant
// types, lifespans, post-logout URIs and metadata written out of band survive.
// current is the client as just read from Hydra; it decides whether the auth
// method changes.
func UpdateHydraOAuthClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, current *HydraOAuthClient, input HydraOAuthClientUpsertInput) (*HydraOAuthClient, error) {
	if current == nil {
		return nil, fmt.Errorf("current oauth client is required")
	}
	ops, err := buildHydraOAuthClientUpdatePatch(current, input)
	if err != nil {
		return nil, err
	}
	return PatchHydraOAuthClient(ctx, hydraConfig, current.ClientID, ops)
}

// SetHydraOAuthClientActive writes metadata.haruki.active with a single patch
// operation aimed at the deepest existing parent, so every other metadata key is
// left byte-for-byte as stored. It does not revoke anything: a caller disabling
// a client must revoke its grants itself.
func SetHydraOAuthClientActive(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string, active bool) (*HydraOAuthClient, error) {
	current, err := GetHydraOAuthClient(ctx, hydraConfig, clientID)
	if err != nil {
		return nil, err
	}
	return PatchHydraOAuthClient(ctx, hydraConfig, clientID, []HydraJSONPatchOp{hydraClientActiveMetadataPatchOp(current.Metadata, active)})
}

// RotateHydraOAuthClientSecret replaces the secret of a confidential client.
// Public clients get ErrHydraPublicClientHasNoSecret and nothing is written.
func RotateHydraOAuthClientSecret(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string, newSecret string) (*HydraOAuthClient, error) {
	newSecret = strings.TrimSpace(newSecret)
	if newSecret == "" {
		return nil, ErrHydraClientSecretRequired
	}
	current, err := GetHydraOAuthClient(ctx, hydraConfig, clientID)
	if err != nil {
		return nil, err
	}
	if HydraClientTypeFromAuthMethod(current.TokenEndpointAuthMethod) == oauthClientTypePublic {
		return nil, ErrHydraPublicClientHasNoSecret
	}
	return PatchHydraOAuthClient(ctx, hydraConfig, clientID, []HydraJSONPatchOp{
		{Op: hydraJSONPatchOpReplace, Path: "/client_secret", Value: newSecret},
	})
}

// HydraOAuthClientSwitchesToConfidential reports whether updating current to
// clientType moves it off token_endpoint_auth_method "none". Such an update must
// carry a new ClientSecret.
func HydraOAuthClientSwitchesToConfidential(current *HydraOAuthClient, clientType string) bool {
	if current == nil {
		return false
	}
	return HydraClientTypeFromAuthMethod(current.TokenEndpointAuthMethod) == oauthClientTypePublic &&
		HydraClientTypeFromAuthMethod(hydraAuthMethodFromClientType(clientType)) == oauthClientTypeConfidential
}

// PatchHydraOAuthClient sends ops as a JSON array to PATCH /admin/clients/{id}.
// Unlike PUT, which replaces the whole client, a patch leaves every member it
// does not name untouched.
func PatchHydraOAuthClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string, ops []HydraJSONPatchOp) (*HydraOAuthClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, fmt.Errorf("client id is required")
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("hydra oauth client patch has no operations")
	}
	for _, op := range ops {
		if op.Op != hydraJSONPatchOpAdd && op.Op != hydraJSONPatchOpReplace {
			return nil, fmt.Errorf("unsupported hydra oauth client patch op %q", op.Op)
		}
		if !strings.HasPrefix(op.Path, "/") {
			return nil, fmt.Errorf("invalid hydra oauth client patch path %q", op.Path)
		}
	}
	requestBody, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("failed to encode hydra oauth client patch: %w", err)
	}
	return sendHydraClientBody(ctx, hydraConfig, http.MethodPatch, "/admin/clients/"+url.PathEscape(clientID), requestBody)
}

func DeleteHydraOAuthClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string) error {
	_, err := sendHydraAdminRequest(ctx, hydraConfig, http.MethodDelete, "/admin/clients/"+url.PathEscape(strings.TrimSpace(clientID)), nil, nil)
	return err
}

func DeleteHydraOAuthTokensByClientID(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string) error {
	query := url.Values{}
	query.Set("client_id", strings.TrimSpace(clientID))
	_, err := sendHydraAdminRequest(ctx, hydraConfig, http.MethodDelete, "/admin/oauth2/tokens", query, nil)
	return err
}

func RevokeHydraConsentSessionsByClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return fmt.Errorf("client id is required")
	}
	query := url.Values{}
	query.Set("client", clientID)
	query.Set("all", "true")
	_, err := sendHydraAdminRequest(ctx, hydraConfig, http.MethodDelete, "/admin/oauth2/auth/sessions/consent", query, nil)
	return err
}

func listHydraOAuthClientsPage(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, pageToken string) ([]HydraOAuthClient, string, error) {
	targetURL, err := hydraConfig.AdminEndpoint("/admin/clients")
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
	if clientID, clientSecret := hydraConfig.ClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := hydraConfig.Do(req)
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

func sendHydraClientRequest(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, method string, endpointPath string, payload map[string]any) (*HydraOAuthClient, error) {
	var requestBody []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode hydra oauth client payload: %w", err)
		}
		requestBody = encoded
	}
	return sendHydraClientBody(ctx, hydraConfig, method, endpointPath, requestBody)
}

func sendHydraClientBody(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, method string, endpointPath string, requestBody []byte) (*HydraOAuthClient, error) {
	responseBody, err := sendHydraClientRequestRaw(ctx, hydraConfig, method, endpointPath, requestBody)
	if err != nil {
		return nil, err
	}
	var client HydraOAuthClient
	if err := json.Unmarshal(responseBody, &client); err != nil {
		return nil, fmt.Errorf("failed to decode hydra oauth client response: %w", err)
	}
	return &client, nil
}

// sendHydraClientRequestRaw sends an encoded JSON body: an object for POST, an
// array of operations for PATCH, nil for bodiless requests.
func sendHydraClientRequestRaw(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, method string, endpointPath string, requestBody []byte) ([]byte, error) {
	targetURL, err := hydraConfig.AdminEndpoint(endpointPath)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create hydra oauth client request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if clientID, clientSecret := hydraConfig.ClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}
	resp, err := hydraConfig.Do(req)
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
	grantTypes := append([]string(nil), input.GrantTypes...)
	if len(grantTypes) == 0 {
		grantTypes = []string{hydraGrantTypeAuthorizationCode, hydraGrantTypeRefreshToken}
	}
	method := hydraAuthMethodFromClientType(clientType)
	payload := map[string]any{
		"client_id":                  strings.TrimSpace(input.ClientID),
		"client_name":                strings.TrimSpace(input.ClientName),
		"redirect_uris":              append([]string(nil), input.RedirectURIs...),
		"grant_types":                grantTypes,
		"response_types":             hydraResponseTypesForGrantTypes(grantTypes),
		"scope":                      strings.Join(input.Scopes, " "),
		"token_endpoint_auth_method": method,
		"metadata":                   buildHydraClientMetadata(input.Active),
	}
	if len(input.PostLogoutRedirectURIs) > 0 {
		payload["post_logout_redirect_uris"] = append([]string(nil), input.PostLogoutRedirectURIs...)
	}
	if method != hydraAuthMethodNone {
		payload["client_secret"] = strings.TrimSpace(input.ClientSecret)
	}
	return payload
}

// buildHydraOAuthClientUpdatePatch emits operations for the admin-owned members
// only. replace is used for members Hydra always serialises; add for those it
// omits when empty (post_logout_redirect_uris, client_secret), since RFC 6902
// add also overwrites an existing member.
func buildHydraOAuthClientUpdatePatch(current *HydraOAuthClient, input HydraOAuthClientUpsertInput) ([]HydraJSONPatchOp, error) {
	redirectURIs := append([]string{}, input.RedirectURIs...)
	ops := []HydraJSONPatchOp{
		{Op: hydraJSONPatchOpReplace, Path: "/client_name", Value: strings.TrimSpace(input.ClientName)},
		{Op: hydraJSONPatchOpReplace, Path: "/scope", Value: strings.Join(input.Scopes, " ")},
		{Op: hydraJSONPatchOpReplace, Path: "/redirect_uris", Value: redirectURIs},
	}

	postLogoutRedirectURIs := input.PostLogoutRedirectURIs
	if len(redirectURIs) == 0 {
		if len(postLogoutRedirectURIs) > 0 {
			return nil, ErrHydraPostLogoutRequiresRedirectURIs
		}
		// Keeping registered post-logout URIs would make Hydra reject the patch.
		postLogoutRedirectURIs = []string{}
	}
	if postLogoutRedirectURIs != nil {
		ops = append(ops, HydraJSONPatchOp{Op: hydraJSONPatchOpAdd, Path: "/post_logout_redirect_uris", Value: append([]string{}, postLogoutRedirectURIs...)})
	}

	if input.GrantTypes != nil {
		grantTypes := append([]string{}, input.GrantTypes...)
		ops = append(ops,
			HydraJSONPatchOp{Op: hydraJSONPatchOpReplace, Path: "/grant_types", Value: grantTypes},
			HydraJSONPatchOp{Op: hydraJSONPatchOpReplace, Path: "/response_types", Value: hydraResponseTypesForGrantTypes(grantTypes)},
		)
	}

	// The auth method is only written when the client type changes, so an out-of-band
	// confidential method such as client_secret_post survives an edit.
	if clientType := strings.TrimSpace(input.ClientType); clientType != "" {
		method := hydraAuthMethodFromClientType(clientType)
		if HydraClientTypeFromAuthMethod(method) != HydraClientTypeFromAuthMethod(current.TokenEndpointAuthMethod) {
			ops = append(ops, HydraJSONPatchOp{Op: hydraJSONPatchOpReplace, Path: "/token_endpoint_auth_method", Value: method})
			if method != hydraAuthMethodNone {
				secret := strings.TrimSpace(input.ClientSecret)
				if secret == "" {
					return nil, ErrHydraClientSecretRequired
				}
				ops = append(ops, HydraJSONPatchOp{Op: hydraJSONPatchOpAdd, Path: "/client_secret", Value: secret})
			}
		}
	}
	return ops, nil
}

// hydraClientActiveMetadataPatchOp targets the deepest existing parent of
// metadata.haruki.active. Re-sending the whole metadata map would round-trip
// unknown values through any (large integers lose precision) and overwrite keys
// changed concurrently.
func hydraClientActiveMetadataPatchOp(metadata map[string]any, active bool) HydraJSONPatchOp {
	if metadata == nil {
		return HydraJSONPatchOp{Op: hydraJSONPatchOpAdd, Path: "/metadata", Value: buildHydraClientMetadata(active)}
	}
	if _, ok := metadata[hydraClientMetadataNamespace].(map[string]any); !ok {
		return HydraJSONPatchOp{Op: hydraJSONPatchOpAdd, Path: "/metadata/" + hydraClientMetadataNamespace, Value: map[string]any{hydraClientActiveKey: active}}
	}
	return HydraJSONPatchOp{Op: hydraJSONPatchOpAdd, Path: "/metadata/" + hydraClientMetadataNamespace + "/" + hydraClientActiveKey, Value: active}
}

// hydraResponseTypesForGrantTypes returns ["code"] exactly when the client may
// use the authorization code grant; a device-only client has none.
func hydraResponseTypesForGrantTypes(grantTypes []string) []string {
	if slices.Contains(grantTypes, hydraGrantTypeAuthorizationCode) {
		return []string{hydraResponseTypeCode}
	}
	return []string{}
}

func hydraAuthMethodFromClientType(clientType string) string {
	switch strings.TrimSpace(clientType) {
	case oauthClientTypeConfidential:
		return hydraAuthMethodClientSecretBasic
	default:
		return hydraAuthMethodNone
	}
}

func buildHydraClientMetadata(active bool) map[string]any {
	return map[string]any{
		hydraClientMetadataNamespace: map[string]any{
			hydraClientActiveKey: active,
		},
	}
}
