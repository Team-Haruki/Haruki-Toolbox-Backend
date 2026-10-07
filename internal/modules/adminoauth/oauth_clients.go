package adminoauth

import (
	"regexp"
	"time"
)

const (
	defaultAdminOAuthClientStatsWindowHours = 24
	maxAdminOAuthClientStatsWindowHours     = 24 * 366 // up to ~1 year (leap-safe); coarse buckets keep point count sane

	defaultAdminOAuthClientPage     = 1
	defaultAdminOAuthClientPageSize = 100
	maxAdminOAuthClientPageSize     = 500

	defaultAdminOAuthClientTrendBucket = "hour"
	adminOAuthClientTrendBucketHour    = "hour"
	adminOAuthClientTrendBucketDay     = "day"

	adminOAuthClientIDMinLen = 3
	adminOAuthClientIDMaxLen = 128
	adminOAuthClientNameMax  = 128

	adminOAuthClientErrorCodePublicClientHasNoSecret = "public_client_has_no_secret"

	// Codes of the 400s a client create or update answers with in updatedData.code.
	adminOAuthClientErrorCodeUnsupportedGrantType            = "unsupported_grant_type"
	adminOAuthClientErrorCodeGrantTypeRequired               = "grant_type_required"
	adminOAuthClientErrorCodeRedirectURIsRequired            = "redirect_uris_required"
	adminOAuthClientErrorCodePostLogoutRequiresRedirectURIs  = "post_logout_requires_redirect_uris"
	adminOAuthClientErrorCodeOfflineAccessRequiresRefresh    = "offline_access_requires_refresh_token"
	adminOAuthClientErrorCodeDeviceRequiresUserRead          = "device_requires_user_read"
	adminOAuthClientErrorCodeDeviceWriteRequiresPublicClient = "device_write_requires_public_client"
	adminOAuthClientErrorCodeInvalidDevicePolicy             = "invalid_device_policy"
)

var adminOAuthClientIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type adminOAuthClientUsageStats struct {
	AuthorizationTotal    int        `json:"authorizationTotal"`
	AuthorizationActive   int        `json:"authorizationActive"`
	AuthorizationInWindow int        `json:"authorizationInWindow"`
	TokenTotal            int        `json:"tokenTotal"`
	TokenActive           int        `json:"tokenActive"`
	TokenIssuedInWindow   int        `json:"tokenIssuedInWindow"`
	LatestAuthorizationAt *time.Time `json:"latestAuthorizationAt,omitzero"`
	LatestTokenIssuedAt   *time.Time `json:"latestTokenIssuedAt,omitzero"`
}

type adminOAuthClientListItem struct {
	ClientID               string                       `json:"clientId"`
	Name                   string                       `json:"name"`
	ClientType             string                       `json:"clientType"`
	Active                 bool                         `json:"active"`
	CreatedAt              time.Time                    `json:"createdAt"`
	RedirectURIs           []string                     `json:"redirectUris"`
	PostLogoutRedirectURIs []string                     `json:"postLogoutRedirectUris"`
	Scopes                 []string                     `json:"scopes"`
	GrantTypes             []string                     `json:"grantTypes"`
	DeviceEnabled          bool                         `json:"deviceEnabled"`
	DevicePolicy           adminOAuthClientDevicePolicy `json:"devicePolicy"`
	Usage                  adminOAuthClientUsageStats   `json:"usage"`
}

// adminOAuthClientDevicePolicy echoes metadata.haruki.device, with defaults for
// members that are not stored.
type adminOAuthClientDevicePolicy struct {
	FirstParty     bool `json:"firstParty"`
	AllowWrite     bool `json:"allowWrite"`
	MaxCodesPer10m int  `json:"maxCodesPer10m"`
}

// adminOAuthClientDevicePolicyPayload is the devicePolicy of a create or update.
// MaxCodesPer10m nil means the default (60).
type adminOAuthClientDevicePolicyPayload struct {
	FirstParty     bool `json:"firstParty"`
	AllowWrite     bool `json:"allowWrite"`
	MaxCodesPer10m *int `json:"maxCodesPer10m"`
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
	// Disabling revokes the client's grants subject by subject. RevokedSubjects
	// counts the subjects Hydra accepted. FailedSubjects lists the failed subjects
	// the admin may see and is never null. RevocationComplete is false when any step
	// failed: listing the grants, a subject (shown or not), or the access-token
	// cleanup. Enabling revokes nothing (0, [], true).
	RevokedSubjects    int      `json:"revokedSubjects"`
	FailedSubjects     []string `json:"failedSubjects"`
	RevocationComplete bool     `json:"revocationComplete"`
}

type adminOAuthClientPayload struct {
	ClientID     string   `json:"clientId"`
	Name         string   `json:"name"`
	ClientType   string   `json:"clientType"`
	RedirectURIs []string `json:"redirectUris"`
	// PostLogoutRedirectURIs: nil (field omitted) keeps the registered list on
	// update; an empty array clears it.
	PostLogoutRedirectURIs []string `json:"postLogoutRedirectUris"`
	Scopes                 []string `json:"scopes"`
	// GrantTypes: nil (field omitted) keeps the registered grant types on update
	// and means authorization_code + refresh_token on create.
	GrantTypes []string `json:"grantTypes"`
	// DevicePolicy: nil (field omitted) keeps the stored policy on update; a
	// non-nil policy replaces all three members.
	DevicePolicy *adminOAuthClientDevicePolicyPayload `json:"devicePolicy"`
}

type adminOAuthClientCreateResponse struct {
	ClientID               string                       `json:"clientId"`
	ClientSecret           string                       `json:"clientSecret"`
	Name                   string                       `json:"name"`
	ClientType             string                       `json:"clientType"`
	Active                 bool                         `json:"active"`
	RedirectURIs           []string                     `json:"redirectUris"`
	PostLogoutRedirectURIs []string                     `json:"postLogoutRedirectUris"`
	Scopes                 []string                     `json:"scopes"`
	GrantTypes             []string                     `json:"grantTypes"`
	DeviceEnabled          bool                         `json:"deviceEnabled"`
	DevicePolicy           adminOAuthClientDevicePolicy `json:"devicePolicy"`
	CreatedAt              time.Time                    `json:"createdAt"`
}

type adminOAuthClientUpdateResponse struct {
	ClientID string `json:"clientId"`
	// ClientSecret is set, once, only when the update switched a public client to
	// confidential.
	ClientSecret           string                       `json:"clientSecret,omitempty"`
	Name                   string                       `json:"name"`
	ClientType             string                       `json:"clientType"`
	Active                 bool                         `json:"active"`
	RedirectURIs           []string                     `json:"redirectUris"`
	PostLogoutRedirectURIs []string                     `json:"postLogoutRedirectUris"`
	Scopes                 []string                     `json:"scopes"`
	GrantTypes             []string                     `json:"grantTypes"`
	DeviceEnabled          bool                         `json:"deviceEnabled"`
	DevicePolicy           adminOAuthClientDevicePolicy `json:"devicePolicy"`
	CreatedAt              time.Time                    `json:"createdAt"`
}

// adminOAuthClientErrorData is the updatedData of an error the frontend keys on.
type adminOAuthClientErrorData struct {
	Code string `json:"code"`
}

// adminOAuthClientPayloadError is a 400 on a client create or update that the
// frontend keys on: Message is the envelope message, Code goes to
// updatedData.code.
type adminOAuthClientPayloadError struct {
	Code    string
	Message string
}

func (e *adminOAuthClientPayloadError) Error() string {
	return e.Message
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
