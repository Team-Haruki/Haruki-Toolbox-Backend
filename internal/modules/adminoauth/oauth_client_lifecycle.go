package adminoauth

import (
	"encoding/json"
	platformPagination "haruki-suite/internal/platform/pagination"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	defaultAdminOAuthClientAuthorizationPage     = 1
	defaultAdminOAuthClientAuthorizationPageSize = 50
	maxAdminOAuthClientAuthorizationPageSize     = 200
)

type adminOAuthClientAuthorizationsFilters struct {
	IncludeRevoked bool
	Page           int
	PageSize       int
}

type adminOAuthClientAuthorizationUser struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Banned bool   `json:"banned"`
}

type adminOAuthClientAuthorizationListItem struct {
	AuthorizationID int                               `json:"authorizationId"`
	User            adminOAuthClientAuthorizationUser `json:"user"`
	Scopes          []string                          `json:"scopes"`
	CreatedAt       time.Time                         `json:"createdAt"`
	Revoked         bool                              `json:"revoked"`
	TokenStats      adminOAuthTokenStats              `json:"tokenStats"`
}

type adminOAuthClientAuthorizationsResponse struct {
	GeneratedAt    time.Time                               `json:"generatedAt"`
	ClientID       string                                  `json:"clientId"`
	ClientName     string                                  `json:"clientName"`
	IncludeRevoked bool                                    `json:"includeRevoked"`
	Page           int                                     `json:"page"`
	PageSize       int                                     `json:"pageSize"`
	Total          int                                     `json:"total"`
	TotalPages     int                                     `json:"totalPages"`
	HasMore        bool                                    `json:"hasMore"`
	Items          []adminOAuthClientAuthorizationListItem `json:"items"`
}

type adminOAuthClientRevokeOptions struct {
	TargetUserID         string `json:"targetUserId,omitempty"`
	RevokeAuthorizations bool   `json:"revokeAuthorizations"`
	RevokeTokens         bool   `json:"revokeTokens"`
}

type adminOAuthClientRevokeResponse struct {
	ClientID              string  `json:"clientId"`
	TargetUserID          *string `json:"targetUserId,omitempty"`
	RevokeAuthorizations  bool    `json:"revokeAuthorizations"`
	RevokeTokens          bool    `json:"revokeTokens"`
	RevokedAuthorizations int     `json:"revokedAuthorizations"`
	RevokedTokens         int     `json:"revokedTokens"`
}

type adminOAuthClientRestoreResponse struct {
	ClientID string `json:"clientId"`
	Active   bool   `json:"active"`
}

func parseAdminOAuthClientAuthorizationsFilters(c fiber.Ctx) (*adminOAuthClientAuthorizationsFilters, error) {
	includeRevoked, err := parseAdminOAuthIncludeRevoked(c.Query("include_revoked"))
	if err != nil {
		return nil, err
	}
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultAdminOAuthClientAuthorizationPage, defaultAdminOAuthClientAuthorizationPageSize, maxAdminOAuthClientAuthorizationPageSize)
	if err != nil {
		return nil, err
	}
	return &adminOAuthClientAuthorizationsFilters{IncludeRevoked: includeRevoked, Page: page, PageSize: pageSize}, nil
}

func parseAdminOAuthClientRevokeOptionsFromJSON(body []byte) (adminOAuthClientRevokeOptions, error) {
	options := adminOAuthClientRevokeOptions{RevokeAuthorizations: true, RevokeTokens: true}
	var payload struct {
		TargetUserID              string `json:"targetUserId"`
		TargetUserIDSnake         string `json:"target_user_id"`
		RevokeAuthorizations      *bool  `json:"revokeAuthorizations"`
		RevokeAuthorizationsSnake *bool  `json:"revoke_authorizations"`
		RevokeTokens              *bool  `json:"revokeTokens"`
		RevokeTokensSnake         *bool  `json:"revoke_tokens"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}
	options.TargetUserID = strings.TrimSpace(firstNonEmptyString(payload.TargetUserID, payload.TargetUserIDSnake))
	if payload.RevokeAuthorizations != nil {
		options.RevokeAuthorizations = *payload.RevokeAuthorizations
	} else if payload.RevokeAuthorizationsSnake != nil {
		options.RevokeAuthorizations = *payload.RevokeAuthorizationsSnake
	}
	if payload.RevokeTokens != nil {
		options.RevokeTokens = *payload.RevokeTokens
	} else if payload.RevokeTokensSnake != nil {
		options.RevokeTokens = *payload.RevokeTokensSnake
	}
	return options, nil
}

func parseAdminOAuthClientRevokeOptionsFromForm(body []byte) (adminOAuthClientRevokeOptions, error) {
	options := adminOAuthClientRevokeOptions{RevokeAuthorizations: true, RevokeTokens: true}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}
	options.TargetUserID = strings.TrimSpace(firstNonEmptyString(values.Get("targetUserId"), values.Get("target_user_id")))
	if raw := strings.TrimSpace(firstNonEmptyString(values.Get("revokeAuthorizations"), values.Get("revoke_authorizations"))); raw != "" {
		v, err := parseBoolLike(raw, "revokeAuthorizations")
		if err != nil {
			return adminOAuthClientRevokeOptions{}, err
		}
		options.RevokeAuthorizations = v
	}
	if raw := strings.TrimSpace(firstNonEmptyString(values.Get("revokeTokens"), values.Get("revoke_tokens"))); raw != "" {
		v, err := parseBoolLike(raw, "revokeTokens")
		if err != nil {
			return adminOAuthClientRevokeOptions{}, err
		}
		options.RevokeTokens = v
	}
	return options, nil
}

func parseAdminOAuthClientRevokeOptions(c fiber.Ctx) (adminOAuthClientRevokeOptions, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return adminOAuthClientRevokeOptions{RevokeAuthorizations: true, RevokeTokens: true}, nil
	}
	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			return parseAdminOAuthClientRevokeOptionsFromJSON(body)
		}
		if looksLikeFormBody(body) {
			return parseAdminOAuthClientRevokeOptionsFromForm(body)
		}
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}
	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case contentTypeApplicationJSON:
		return parseAdminOAuthClientRevokeOptionsFromJSON(body)
	case contentTypeApplicationFormURLEncoded:
		return parseAdminOAuthClientRevokeOptionsFromForm(body)
	default:
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseBoolLike(raw string, fieldName string) (bool, error) {
	if parsed, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
		return parsed, nil
	}
	return false, fiber.NewError(fiber.StatusBadRequest, "invalid "+fieldName)
}
