package adminusers

import (
	"encoding/json"
	adminCoreModule "haruki-suite/internal/modules/admincore"
	"mime"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func parseAdminOAuthIncludeRevoked(raw string) (bool, error) {
	includeRevoked, err := adminCoreModule.ParseOptionalBoolField(raw, "include_revoked")
	if err != nil {
		return false, err
	}
	if includeRevoked == nil {
		return false, nil
	}
	return *includeRevoked, nil
}

func looksLikeJSONBody(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func looksLikeFormBody(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "=")
}

func parseRevokeOAuthClientIDFromJSON(body []byte) (string, error) {
	var payload struct {
		ClientID      string `json:"clientId"`
		ClientIDSnake string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	clientID := strings.TrimSpace(payload.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(payload.ClientIDSnake)
	}
	return clientID, nil
}

func parseRevokeOAuthClientIDFromForm(body []byte) (string, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}

	clientID := strings.TrimSpace(values.Get("client_id"))
	if clientID == "" {
		clientID = strings.TrimSpace(values.Get("clientId"))
	}
	return clientID, nil
}

func parseAdminRevokeOAuthClientID(c fiber.Ctx) (string, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return "", nil
	}

	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			return parseRevokeOAuthClientIDFromJSON(body)
		}
		if looksLikeFormBody(body) {
			return parseRevokeOAuthClientIDFromForm(body)
		}
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case contentTypeApplicationJSON:
		return parseRevokeOAuthClientIDFromJSON(body)
	case contentTypeApplicationFormURLEncoded:
		return parseRevokeOAuthClientIDFromForm(body)
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
}
