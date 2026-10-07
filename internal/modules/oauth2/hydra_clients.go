package oauth2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
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

	// metadata.haruki.device holds the per-client device-flow policy.
	hydraClientDeviceKey               = "device"
	hydraClientDeviceFirstPartyKey     = "first_party"
	hydraClientDeviceAllowWriteKey     = "allow_write"
	hydraClientDeviceMaxCodesPer10mKey = "max_codes_per_10m"

	HydraGrantTypeAuthorizationCode = "authorization_code"
	HydraGrantTypeRefreshToken      = "refresh_token"
	// HydraGrantTypeDeviceCode is the RFC 8628 device authorization grant.
	HydraGrantTypeDeviceCode = "urn:ietf:params:oauth:grant-type:device_code"

	// The device policy's per-client cap on device codes issued per 10 minutes.
	HydraDeviceMaxCodesPer10mDefault = 60
	HydraDeviceMaxCodesPer10mMin     = 1
	HydraDeviceMaxCodesPer10mMax     = 600

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
	// DevicePolicy is written to metadata.haruki.device when non-nil, on create and
	// on update alike; nil writes nothing, so an update keeps the stored policy.
	DevicePolicy *HydraOAuthClientDevicePolicy
	// Active only seeds metadata on create; SetHydraOAuthClientActive changes it.
	Active bool
}

// HydraOAuthClientDevicePolicy is a client's device-flow policy, stored as
// metadata.haruki.device = {"first_party": bool, "allow_write": bool,
// "max_codes_per_10m": int}. Hydra ignores it; the backend enforces it.
type HydraOAuthClientDevicePolicy struct {
	// FirstParty is only a badge on the approval card, shown for confidential
	// clients alone: anyone can use a public client's client_id.
	FirstParty bool
	// AllowWrite lets the client's device flows be granted game-data:write. Only
	// public clients may have it.
	AllowWrite bool
	// MaxCodesPer10m caps the device codes issued to the client per 10 minutes.
	MaxCodesPer10m int
}

// DefaultHydraOAuthClientDevicePolicy is the policy of a client with no stored one.
func DefaultHydraOAuthClientDevicePolicy() HydraOAuthClientDevicePolicy {
	return HydraOAuthClientDevicePolicy{MaxCodesPer10m: HydraDeviceMaxCodesPer10mDefault}
}

// DefaultHydraOAuthClientGrantTypes are the grant types a client is created with
// when none are given.
func DefaultHydraOAuthClientGrantTypes() []string {
	return []string{HydraGrantTypeAuthorizationCode, HydraGrantTypeRefreshToken}
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

// HydraOAuthClientDeviceEnabled reports whether the client holds the device
// authorization grant.
func HydraOAuthClientDeviceEnabled(client *HydraOAuthClient) bool {
	return client != nil && slices.Contains(client.GrantTypes, HydraGrantTypeDeviceCode)
}

// HydraOAuthClientDevicePolicyOf reads metadata.haruki.device. Missing or
// malformed members, and a max_codes_per_10m outside its range, fall back to
// DefaultHydraOAuthClientDevicePolicy member by member.
func HydraOAuthClientDevicePolicyOf(client *HydraOAuthClient) HydraOAuthClientDevicePolicy {
	policy := DefaultHydraOAuthClientDevicePolicy()
	if client == nil {
		return policy
	}
	namespace, _ := client.Metadata[hydraClientMetadataNamespace].(map[string]any)
	device, _ := namespace[hydraClientDeviceKey].(map[string]any)
	if firstParty, ok := device[hydraClientDeviceFirstPartyKey].(bool); ok {
		policy.FirstParty = firstParty
	}
	if allowWrite, ok := device[hydraClientDeviceAllowWriteKey].(bool); ok {
		policy.AllowWrite = allowWrite
	}
	if maxCodes, ok := hydraOAuthClientStoredMaxCodesPer10m(client); ok {
		policy.MaxCodesPer10m = maxCodes
	}
	return policy
}

// hydraOAuthClientStoredMaxCodesPer10m returns a valid stored
// metadata.haruki.device.max_codes_per_10m.
func hydraOAuthClientStoredMaxCodesPer10m(client *HydraOAuthClient) (int, bool) {
	if client == nil {
		return 0, false
	}
	namespace, _ := client.Metadata[hydraClientMetadataNamespace].(map[string]any)
	device, _ := namespace[hydraClientDeviceKey].(map[string]any)
	// Metadata decodes into any, so a stored integer arrives as float64.
	maxCodes, ok := device[hydraClientDeviceMaxCodesPer10mKey].(float64)
	if !ok || maxCodes != math.Trunc(maxCodes) || maxCodes < HydraDeviceMaxCodesPer10mMin || maxCodes > HydraDeviceMaxCodesPer10mMax {
		return 0, false
	}
	return int(maxCodes), true
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
	return PatchHydraOAuthClient(ctx, hydraConfig, clientID, hydraClientMetadataPatchOps(current.Metadata, []string{hydraClientMetadataNamespace}, []hydraClientMetadataMember{
		{key: hydraClientActiveKey, value: active},
	}))
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

// DeleteHydraOAuthClient deletes the client. Hydra's foreign keys cascade the delete
// to the client's consent sessions, access tokens and refresh tokens.
func DeleteHydraOAuthClient(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string) error {
	_, err := sendHydraAdminRequest(ctx, hydraConfig, http.MethodDelete, "/admin/clients/"+url.PathEscape(strings.TrimSpace(clientID)), nil, nil)
	return err
}

// DeleteHydraOAuthTokensByClientID deletes the client's access tokens only. Hydra
// keeps its refresh tokens, and they go on minting new access tokens. This is a
// supplement: revoking the consent sessions (RevokeHydraConsentSessionsForSubjects)
// is what invalidates access and refresh tokens together.
func DeleteHydraOAuthTokensByClientID(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string) error {
	query := url.Values{}
	query.Set("client_id", strings.TrimSpace(clientID))
	_, err := sendHydraAdminRequest(ctx, hydraConfig, http.MethodDelete, "/admin/oauth2/tokens", query, nil)
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
		grantTypes = DefaultHydraOAuthClientGrantTypes()
	}
	method := hydraAuthMethodFromClientType(clientType)
	payload := map[string]any{
		"client_id":                  strings.TrimSpace(input.ClientID),
		"client_name":                strings.TrimSpace(input.ClientName),
		"redirect_uris":              append([]string{}, input.RedirectURIs...),
		"grant_types":                grantTypes,
		"response_types":             hydraResponseTypesForGrantTypes(grantTypes),
		"scope":                      strings.Join(input.Scopes, " "),
		"token_endpoint_auth_method": method,
		"metadata":                   buildHydraClientMetadata(input.Active, input.DevicePolicy),
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

	if input.DevicePolicy != nil {
		ops = append(ops, hydraClientMetadataPatchOps(current.Metadata, []string{hydraClientMetadataNamespace, hydraClientDeviceKey}, hydraClientDevicePolicyMembers(*input.DevicePolicy))...)
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

// hydraClientMetadataMember is one metadata member a patch writes.
type hydraClientMetadataMember struct {
	key   string
	value any
}

// hydraClientMetadataPatchOps writes members into the metadata object at
// parents (e.g. haruki, device) with add operations aimed at the deepest
// existing parent: one op per member when the whole path exists, otherwise a
// single op creating the first missing object with the rest nested inside.
// Re-sending the whole metadata map would round-trip unknown values through any
// (large integers lose precision) and overwrite keys changed concurrently.
func hydraClientMetadataPatchOps(metadata map[string]any, parents []string, members []hydraClientMetadataMember) []HydraJSONPatchOp {
	if metadata == nil {
		return []HydraJSONPatchOp{{Op: hydraJSONPatchOpAdd, Path: "/metadata", Value: nestHydraClientMetadata(parents, members)}}
	}
	path := "/metadata"
	node := metadata
	for i, parent := range parents {
		next, ok := node[parent].(map[string]any)
		if !ok {
			return []HydraJSONPatchOp{{Op: hydraJSONPatchOpAdd, Path: path + "/" + parent, Value: nestHydraClientMetadata(parents[i+1:], members)}}
		}
		path += "/" + parent
		node = next
	}
	ops := make([]HydraJSONPatchOp, 0, len(members))
	for _, member := range members {
		ops = append(ops, HydraJSONPatchOp{Op: hydraJSONPatchOpAdd, Path: path + "/" + member.key, Value: member.value})
	}
	return ops
}

func nestHydraClientMetadata(parents []string, members []hydraClientMetadataMember) map[string]any {
	value := make(map[string]any, len(members))
	for _, member := range members {
		value[member.key] = member.value
	}
	for i := len(parents) - 1; i >= 0; i-- {
		value = map[string]any{parents[i]: value}
	}
	return value
}

func hydraClientDevicePolicyMembers(policy HydraOAuthClientDevicePolicy) []hydraClientMetadataMember {
	return []hydraClientMetadataMember{
		{key: hydraClientDeviceFirstPartyKey, value: policy.FirstParty},
		{key: hydraClientDeviceAllowWriteKey, value: policy.AllowWrite},
		{key: hydraClientDeviceMaxCodesPer10mKey, value: policy.MaxCodesPer10m},
	}
}

// hydraResponseTypesForGrantTypes returns ["code"] exactly when the client may
// use the authorization code grant; a device-only client has none.
func hydraResponseTypesForGrantTypes(grantTypes []string) []string {
	if slices.Contains(grantTypes, HydraGrantTypeAuthorizationCode) {
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

func buildHydraClientMetadata(active bool, devicePolicy *HydraOAuthClientDevicePolicy) map[string]any {
	namespace := map[string]any{hydraClientActiveKey: active}
	if devicePolicy != nil {
		namespace[hydraClientDeviceKey] = nestHydraClientMetadata(nil, hydraClientDevicePolicyMembers(*devicePolicy))
	}
	return map[string]any{hydraClientMetadataNamespace: namespace}
}
