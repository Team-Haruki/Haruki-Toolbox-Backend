package oauth2

import (
	"fmt"
)

type hydraOAuthClientDetails struct {
	ClientID   string   `json:"client_id"`
	ClientName string   `json:"client_name"`
	GrantTypes []string `json:"grant_types"`
	Scope      string   `json:"scope"`
}

type hydraLoginRequestResponse struct {
	Challenge                    string                  `json:"challenge"`
	Skip                         bool                    `json:"skip"`
	Subject                      string                  `json:"subject"`
	RequestURL                   string                  `json:"request_url"`
	RequestedScope               []string                `json:"requested_scope"`
	RequestedAccessTokenAudience []string                `json:"requested_access_token_audience"`
	Client                       hydraOAuthClientDetails `json:"client"`
}

type hydraConsentRequestResponse struct {
	Challenge                    string                  `json:"challenge"`
	Skip                         bool                    `json:"skip"`
	Subject                      string                  `json:"subject"`
	RequestURL                   string                  `json:"request_url"`
	RequestedScope               []string                `json:"requested_scope"`
	RequestedAccessTokenAudience []string                `json:"requested_access_token_audience"`
	Client                       hydraOAuthClientDetails `json:"client"`
}

// hydraLogoutRequestResponse is what Hydra returns for a logout challenge.
//
// Client is a pointer because Hydra omits it for a logout the user started on
// the OP itself rather than one an RP initiated — the frontend uses its presence
// to decide between naming the application and using generic wording.
type hydraLogoutRequestResponse struct {
	Challenge   string                   `json:"challenge"`
	Subject     string                   `json:"subject"`
	SessionID   string                   `json:"sid"`
	RequestURL  string                   `json:"request_url"`
	RPInitiated bool                     `json:"rp_initiated"`
	Client      *hydraOAuthClientDetails `json:"client,omitempty"`
}

// hydraLogoutPayload carries the challenge for accept and reject alike; neither
// takes any other field.
type hydraLogoutPayload struct {
	LogoutChallenge string `json:"logoutChallenge"`
}

type hydraRedirectResponse struct {
	RedirectTo string `json:"redirect_to"`
}

type hydraLoginAcceptPayload struct {
	LoginChallenge string `json:"loginChallenge"`
	Remember       bool   `json:"remember"`
	RememberFor    int64  `json:"rememberFor"`
	ACR            string `json:"acr"`
}

type hydraLoginRejectPayload struct {
	LoginChallenge   string `json:"loginChallenge"`
	Error            string `json:"error"`
	ErrorDescription string `json:"errorDescription"`
	StatusCode       int    `json:"statusCode"`
}

type hydraConsentAcceptPayload struct {
	ConsentChallenge         string   `json:"consentChallenge"`
	GrantScope               []string `json:"grantScope"`
	GrantAccessTokenAudience []string `json:"grantAccessTokenAudience"`
	Remember                 bool     `json:"remember"`
	RememberFor              int64    `json:"rememberFor"`
}

type hydraConsentRejectPayload struct {
	ConsentChallenge string `json:"consentChallenge"`
	Error            string `json:"error"`
	ErrorDescription string `json:"errorDescription"`
	StatusCode       int    `json:"statusCode"`
}

type hydraLegacyConsentPayload struct {
	ConsentChallenge         string   `json:"consentChallenge"`
	Approved                 bool     `json:"approved"`
	Scope                    string   `json:"scope"`
	GrantScope               []string `json:"grantScope"`
	GrantAccessTokenAudience []string `json:"grantAccessTokenAudience"`
	Remember                 bool     `json:"remember"`
	RememberFor              int64    `json:"rememberFor"`
}

type hydraErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Message          string `json:"message"`
}

type hydraRequestError struct {
	Status  int
	Message string
}

func (e *hydraRequestError) Error() string {
	return fmt.Sprintf("hydra request failed with status %d: %s", e.Status, e.Message)
}
