package adminoauth

import (
	"encoding/json"
	adminCoreModule "haruki-suite/internal/modules/admincore"
	"mime"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func parseAdminOAuthClientActiveFromJSON(body []byte) (*bool, error) {
	var payload struct {
		Active *bool `json:"active"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}
	if payload.Active == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "active is required")
	}
	return payload.Active, nil
}

func parseAdminOAuthClientActiveFromForm(body []byte) (*bool, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}

	active, err := adminCoreModule.ParseOptionalBoolField(values.Get("active"), "active")
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "active is required")
	}
	return active, nil
}

func parseAdminOAuthClientActiveValue(c fiber.Ctx) (bool, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return false, fiber.NewError(fiber.StatusBadRequest, "active is required")
	}

	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			active, err := parseAdminOAuthClientActiveFromJSON(body)
			if err != nil {
				return false, err
			}
			return *active, nil
		}
		if looksLikeFormBody(body) {
			active, err := parseAdminOAuthClientActiveFromForm(body)
			if err != nil {
				return false, err
			}
			return *active, nil
		}
		return false, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return false, fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case contentTypeApplicationJSON:
		active, err := parseAdminOAuthClientActiveFromJSON(body)
		if err != nil {
			return false, err
		}
		return *active, nil
	case contentTypeApplicationFormURLEncoded:
		active, err := parseAdminOAuthClientActiveFromForm(body)
		if err != nil {
			return false, err
		}
		return *active, nil
	default:
		return false, fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
}

func parseAdminOAuthClientDeleteOptionsFromJSON(body []byte) (adminOAuthClientDeleteOptions, error) {
	options := adminOAuthClientDeleteOptions{
		DeleteAuthorizations: true,
		DeleteTokens:         true,
	}

	var payload struct {
		DeleteAuthorizations      *bool `json:"deleteAuthorizations"`
		DeleteAuthorizationsSnake *bool `json:"delete_authorizations"`
		DeleteTokens              *bool `json:"deleteTokens"`
		DeleteTokensSnake         *bool `json:"delete_tokens"`
		RevokeAuthorizations      *bool `json:"revokeAuthorizations"`
		RevokeAuthorizationsSnake *bool `json:"revoke_authorizations"`
		RevokeTokens              *bool `json:"revokeTokens"`
		RevokeTokensSnake         *bool `json:"revoke_tokens"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	if payload.DeleteAuthorizations != nil {
		options.DeleteAuthorizations = *payload.DeleteAuthorizations
	} else if payload.DeleteAuthorizationsSnake != nil {
		options.DeleteAuthorizations = *payload.DeleteAuthorizationsSnake
	} else if payload.RevokeAuthorizations != nil {
		options.DeleteAuthorizations = *payload.RevokeAuthorizations
	} else if payload.RevokeAuthorizationsSnake != nil {
		options.DeleteAuthorizations = *payload.RevokeAuthorizationsSnake
	}
	if payload.DeleteTokens != nil {
		options.DeleteTokens = *payload.DeleteTokens
	} else if payload.DeleteTokensSnake != nil {
		options.DeleteTokens = *payload.DeleteTokensSnake
	} else if payload.RevokeTokens != nil {
		options.DeleteTokens = *payload.RevokeTokens
	} else if payload.RevokeTokensSnake != nil {
		options.DeleteTokens = *payload.RevokeTokensSnake
	}
	return options, nil
}

func parseAdminOAuthClientDeleteOptionsFromForm(body []byte) (adminOAuthClientDeleteOptions, error) {
	options := adminOAuthClientDeleteOptions{
		DeleteAuthorizations: true,
		DeleteTokens:         true,
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}

	deleteAuthorizationsRaw := strings.TrimSpace(values.Get("delete_authorizations"))
	if deleteAuthorizationsRaw == "" {
		deleteAuthorizationsRaw = strings.TrimSpace(values.Get("deleteAuthorizations"))
	}
	if deleteAuthorizationsRaw == "" {
		deleteAuthorizationsRaw = strings.TrimSpace(values.Get("revoke_authorizations"))
	}
	if deleteAuthorizationsRaw == "" {
		deleteAuthorizationsRaw = strings.TrimSpace(values.Get("revokeAuthorizations"))
	}
	deleteAuthorizations, err := adminCoreModule.ParseOptionalBoolField(deleteAuthorizationsRaw, "delete_authorizations")
	if err != nil {
		return adminOAuthClientDeleteOptions{}, err
	}
	if deleteAuthorizations != nil {
		options.DeleteAuthorizations = *deleteAuthorizations
	}

	deleteTokensRaw := strings.TrimSpace(values.Get("delete_tokens"))
	if deleteTokensRaw == "" {
		deleteTokensRaw = strings.TrimSpace(values.Get("deleteTokens"))
	}
	if deleteTokensRaw == "" {
		deleteTokensRaw = strings.TrimSpace(values.Get("revoke_tokens"))
	}
	if deleteTokensRaw == "" {
		deleteTokensRaw = strings.TrimSpace(values.Get("revokeTokens"))
	}
	deleteTokens, err := adminCoreModule.ParseOptionalBoolField(deleteTokensRaw, "delete_tokens")
	if err != nil {
		return adminOAuthClientDeleteOptions{}, err
	}
	if deleteTokens != nil {
		options.DeleteTokens = *deleteTokens
	}
	return options, nil
}

func parseAdminOAuthClientDeleteOptions(c fiber.Ctx) (adminOAuthClientDeleteOptions, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return adminOAuthClientDeleteOptions{
			DeleteAuthorizations: true,
			DeleteTokens:         true,
		}, nil
	}

	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			return parseAdminOAuthClientDeleteOptionsFromJSON(body)
		}
		if looksLikeFormBody(body) {
			return parseAdminOAuthClientDeleteOptionsFromForm(body)
		}
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case contentTypeApplicationJSON:
		return parseAdminOAuthClientDeleteOptionsFromJSON(body)
	case contentTypeApplicationFormURLEncoded:
		return parseAdminOAuthClientDeleteOptionsFromForm(body)
	default:
		return adminOAuthClientDeleteOptions{}, fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
}
