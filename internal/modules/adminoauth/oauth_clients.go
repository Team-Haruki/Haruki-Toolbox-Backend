package adminoauth

import (
	"regexp"
	"time"
)

const (
	defaultAdminOAuthClientStatsWindowHours = 24
	maxAdminOAuthClientStatsWindowHours     = 24 * 30

	defaultAdminOAuthClientPage     = 1
	defaultAdminOAuthClientPageSize = 100
	maxAdminOAuthClientPageSize     = 500

	defaultAdminOAuthClientTrendBucket = "hour"
	adminOAuthClientTrendBucketHour    = "hour"
	adminOAuthClientTrendBucketDay     = "day"

	adminOAuthClientIDMinLen = 3
	adminOAuthClientIDMaxLen = 128
	adminOAuthClientNameMax  = 128
)

var adminOAuthClientIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type adminOAuthClientUsageStats struct {
	AuthorizationTotal    int        `json:"authorizationTotal"`
	AuthorizationActive   int        `json:"authorizationActive"`
	AuthorizationInWindow int        `json:"authorizationInWindow"`
	TokenTotal            int        `json:"tokenTotal"`
	TokenActive           int        `json:"tokenActive"`
	TokenIssuedInWindow   int        `json:"tokenIssuedInWindow"`
	LatestAuthorizationAt *time.Time `json:"latestAuthorizationAt,omitempty"`
	LatestTokenIssuedAt   *time.Time `json:"latestTokenIssuedAt,omitempty"`
}

type adminOAuthClientListItem struct {
	ClientID     string                     `json:"clientId"`
	Name         string                     `json:"name"`
	ClientType   string                     `json:"clientType"`
	Active       bool                       `json:"active"`
	CreatedAt    time.Time                  `json:"createdAt"`
	RedirectURIs []string                   `json:"redirectUris"`
	Scopes       []string                   `json:"scopes"`
	Usage        adminOAuthClientUsageStats `json:"usage"`
}

type adminOAuthClientListResponse struct {
	GeneratedAt     time.Time                  `json:"generatedAt"`
	WindowHours     int                        `json:"windowHours"`
	WindowStart     time.Time                  `json:"windowStart"`
	WindowEnd       time.Time                  `json:"windowEnd"`
	IncludeInactive bool                       `json:"includeInactive"`
	Page            int                        `json:"page"`
	PageSize        int                        `json:"pageSize"`
	Total           int                        `json:"total"`
	TotalPages      int                        `json:"totalPages"`
	HasMore         bool                       `json:"hasMore"`
	Items           []adminOAuthClientListItem `json:"items"`
}

type adminOAuthClientActiveResponse struct {
	ClientID string `json:"clientId"`
	Active   bool   `json:"active"`
}

type adminOAuthClientPayload struct {
	ClientID     string   `json:"clientId"`
	Name         string   `json:"name"`
	ClientType   string   `json:"clientType"`
	RedirectURIs []string `json:"redirectUris"`
	Scopes       []string `json:"scopes"`
}

type adminOAuthClientCreateResponse struct {
	ClientID     string    `json:"clientId"`
	ClientSecret string    `json:"clientSecret"`
	Name         string    `json:"name"`
	ClientType   string    `json:"clientType"`
	Active       bool      `json:"active"`
	RedirectURIs []string  `json:"redirectUris"`
	Scopes       []string  `json:"scopes"`
	CreatedAt    time.Time `json:"createdAt"`
}

type adminOAuthClientUpdateResponse struct {
	ClientID     string    `json:"clientId"`
	Name         string    `json:"name"`
	ClientType   string    `json:"clientType"`
	Active       bool      `json:"active"`
	RedirectURIs []string  `json:"redirectUris"`
	Scopes       []string  `json:"scopes"`
	CreatedAt    time.Time `json:"createdAt"`
}

type adminOAuthClientRotateSecretResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type adminOAuthClientDeleteOptions struct {
	DeleteAuthorizations bool `json:"deleteAuthorizations"`
	DeleteTokens         bool `json:"deleteTokens"`
}

type adminOAuthClientDeleteResponse struct {
	ClientID              string `json:"clientId"`
	DeleteAuthorizations  bool   `json:"deleteAuthorizations"`
	DeleteTokens          bool   `json:"deleteTokens"`
	DeletedAuthorizations int    `json:"deletedAuthorizations"`
	DeletedTokens         int    `json:"deletedTokens"`
	RevokeAuthorizations  bool   `json:"revokeAuthorizations"`
	RevokeTokens          bool   `json:"revokeTokens"`
	RevokedAuthorizations int    `json:"revokedAuthorizations"`
	RevokedTokens         int    `json:"revokedTokens"`
}

type adminOAuthClientStatisticsFilters struct {
	From   time.Time
	To     time.Time
	Bucket string
}

type adminOAuthClientStatisticsSummary struct {
	AuthorizationTotal          int `json:"authorizationTotal"`
	AuthorizationActive         int `json:"authorizationActive"`
	AuthorizationRevoked        int `json:"authorizationRevoked"`
	AuthorizationCreatedInRange int `json:"authorizationCreatedInRange"`
	TokenTotal                  int `json:"tokenTotal"`
	TokenActive                 int `json:"tokenActive"`
	TokenRevoked                int `json:"tokenRevoked"`
	TokenIssuedInRange          int `json:"tokenIssuedInRange"`
}

type adminOAuthClientTrendPoint struct {
	BucketStart          time.Time `json:"bucketStart"`
	AuthorizationCreated int       `json:"authorizationCreated"`
	TokenIssued          int       `json:"tokenIssued"`
}

type adminOAuthClientStatisticsResponse struct {
	GeneratedAt time.Time                         `json:"generatedAt"`
	ClientID    string                            `json:"clientId"`
	ClientName  string                            `json:"clientName"`
	ClientType  string                            `json:"clientType"`
	Active      bool                              `json:"active"`
	From        time.Time                         `json:"from"`
	To          time.Time                         `json:"to"`
	Bucket      string                            `json:"bucket"`
	Summary     adminOAuthClientStatisticsSummary `json:"summary"`
	Trend       []adminOAuthClientTrendPoint      `json:"trend"`
}
