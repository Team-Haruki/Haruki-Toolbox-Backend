package oauth2

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func formValue(formValues url.Values, key string) string {
	return strings.TrimSpace(formValues.Get(key))
}

func isFormURLEncodedContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return false
	}
	return strings.EqualFold(mediaType, "application/x-www-form-urlencoded")
}

func parseOAuthFormBody(body []byte) (url.Values, error) {
	if len(body) == 0 {
		return make(url.Values), nil
	}
	return url.ParseQuery(string(body))
}

type oauthClientAuthentication struct {
	ClientID     string
	ClientSecret string
}

type oauthErrorResponse struct {
	Status               int
	Code                 string
	Description          string
	BasicChallengeNeeded bool
}

func parseBasicAuthorizationValue(authHeader string) (clientID, clientSecret string, presented bool, err error) {
	parts := strings.SplitN(strings.TrimSpace(authHeader), " ", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false, nil
	}
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return "", "", true, fmt.Errorf("unsupported authorization scheme")
	}

	decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if decodeErr != nil {
		return "", "", true, fmt.Errorf("invalid basic auth encoding")
	}

	credential := string(decoded)
	sep := strings.Index(credential, ":")
	if sep < 0 {
		return "", "", true, fmt.Errorf("invalid basic auth credential format")
	}

	rawClientID := credential[:sep]
	rawClientSecret := credential[sep+1:]

	decodedClientID, err := url.QueryUnescape(rawClientID)
	if err != nil {
		return "", "", true, fmt.Errorf("invalid basic auth client_id")
	}
	decodedClientSecret, err := url.QueryUnescape(rawClientSecret)
	if err != nil {
		return "", "", true, fmt.Errorf("invalid basic auth client_secret")
	}

	return strings.TrimSpace(decodedClientID), decodedClientSecret, true, nil
}

func extractClientAuthentication(c fiber.Ctx, formValues url.Values) (oauthClientAuthentication, *oauthErrorResponse) {
	bodyClientID := formValue(formValues, "client_id")
	bodyClientSecret := formValues.Get("client_secret")
	_, bodySecretPresented := formValues["client_secret"]
	bodyCredentialPresented := bodyClientID != "" || bodySecretPresented

	basicClientID, basicClientSecret, basicPresented, basicErr := parseBasicAuthorizationValue(c.Get("Authorization"))
	if basicErr != nil {
		return oauthClientAuthentication{}, &oauthErrorResponse{
			Status:               fiber.StatusUnauthorized,
			Code:                 "invalid_client",
			Description:          "invalid client authentication",
			BasicChallengeNeeded: true,
		}
	}

	if basicPresented && bodyCredentialPresented {
		return oauthClientAuthentication{}, &oauthErrorResponse{
			Status:      fiber.StatusBadRequest,
			Code:        "invalid_request",
			Description: "multiple client authentication methods used",
		}
	}

	if basicPresented {
		return oauthClientAuthentication{
			ClientID:     basicClientID,
			ClientSecret: basicClientSecret,
		}, nil
	}

	return oauthClientAuthentication{
		ClientID:     bodyClientID,
		ClientSecret: bodyClientSecret,
	}, nil
}

func parseScopeList(scope string) []string {
	return strings.Fields(scope)
}

func isScopeSubset(requested, granted []string) bool {
	grantedSet := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		grantedSet[s] = struct{}{}
	}
	for _, s := range requested {
		if _, ok := grantedSet[s]; !ok {
			return false
		}
	}
	return true
}

func respondOAuthError(c fiber.Ctx, e oauthErrorResponse) error {
	if e.BasicChallengeNeeded {
		c.Set("WWW-Authenticate", `Basic realm="oauth2"`)
	}
	return oauthError(c, e.Status, e.Code, e.Description)
}

func oauthError(c fiber.Ctx, status int, errorCode, description string) error {
	return c.Status(status).JSON(fiber.Map{
		"error":             errorCode,
		"error_description": description,
	})
}

// buildRedirectURL constructs a redirect URI with the given query params and optional state.
func buildRedirectURL(baseURI, state string, params map[string]string) string {
	parsed, err := url.Parse(baseURI)
	if err != nil {
		// Fallback to legacy behavior if baseURI itself is invalid.
		u := baseURI
		first := !strings.Contains(baseURI, "?")
		for k, v := range params {
			if first {
				u += "?"
				first = false
			} else {
				u += "&"
			}
			u += url.QueryEscape(k) + "=" + url.QueryEscape(v)
		}
		if state != "" {
			if first {
				u += "?"
			} else {
				u += "&"
			}
			u += "state=" + url.QueryEscape(state)
		}
		return u
	}

	query := parsed.Query()
	for k, v := range params {
		query.Set(k, v)
	}
	if state != "" {
		query.Set("state", state)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
