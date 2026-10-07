package adminusers

import "time"

type adminOAuthTokenStats struct {
	Exact           bool       `json:"exact"`
	Total           int        `json:"total"`
	Active          int        `json:"active"`
	Revoked         int        `json:"revoked"`
	LatestIssuedAt  *time.Time `json:"latestIssuedAt,omitzero"`
	LatestExpiresAt *time.Time `json:"latestExpiresAt,omitzero"`
}

type adminOAuthAuthorizationListItem struct {
	AuthorizationID  int                  `json:"authorizationId"`
	ConsentRequestID string               `json:"consentRequestId,omitempty"`
	ClientID         string               `json:"clientId"`
	ClientName       string               `json:"clientName"`
	ClientType       string               `json:"clientType"`
	ClientActive     bool                 `json:"clientActive"`
	Scopes           []string             `json:"scopes"`
	CreatedAt        time.Time            `json:"createdAt"`
	Revoked          bool                 `json:"revoked"`
	TokenStats       adminOAuthTokenStats `json:"tokenStats"`
	// FlowType is "device" or "browser"; DeviceLabel is "" for browser
	// authorizations. Read-only: admins revoke per client, not per device.
	FlowType    string `json:"flowType"`
	DeviceLabel string `json:"deviceLabel"`
}

type adminOAuthAuthorizationListResponse struct {
	GeneratedAt    time.Time                         `json:"generatedAt"`
	UserID         string                            `json:"userId"`
	IncludeRevoked bool                              `json:"includeRevoked"`
	Total          int                               `json:"total"`
	Items          []adminOAuthAuthorizationListItem `json:"items"`
}

type adminRevokeOAuthResponse struct {
	UserID                     string  `json:"userId"`
	ClientID                   *string `json:"clientId,omitzero"`
	RevokedAuthorizations      int     `json:"revokedAuthorizations"`
	RevokedAuthorizationsExact *bool   `json:"revokedAuthorizationsExact,omitzero"`
	RevokedTokens              int     `json:"revokedTokens"`
	RevokedTokensExact         *bool   `json:"revokedTokensExact,omitzero"`
}
