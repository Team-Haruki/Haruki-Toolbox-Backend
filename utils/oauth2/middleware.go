package oauth2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	platformAuthHeader "haruki-suite/internal/platform/authheader"
	"haruki-suite/utils/database/postgresql"
	harukiLogger "haruki-suite/utils/logger"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

type hydraIntrospectionResponse struct {
	Active   bool   `json:"active"`
	Subject  string `json:"sub"`
	Username string `json:"username"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
	Exp      int64  `json:"exp"`
	Nbf      int64  `json:"nbf"`
	Iat      int64  `json:"iat"`
}

type hydraIntrospectionError struct {
	Status  int
	Message string
}

func (e *hydraIntrospectionError) Error() string {
	return fmt.Sprintf("hydra introspection failed with status %d: %s", e.Status, e.Message)
}

func escapeBearerAuthParam(v string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"")
	return replacer.Replace(v)
}

func buildBearerChallenge(errorCode, description, scope string) string {
	parts := []string{`Bearer realm="haruki-toolbox"`}
	if errorCode != "" {
		parts = append(parts, fmt.Sprintf(`error="%s"`, escapeBearerAuthParam(errorCode)))
	}
	if description != "" {
		parts = append(parts, fmt.Sprintf(`error_description="%s"`, escapeBearerAuthParam(description)))
	}
	if scope != "" {
		parts = append(parts, fmt.Sprintf(`scope="%s"`, escapeBearerAuthParam(scope)))
	}
	return strings.Join(parts, ", ")
}

func VerifyOAuth2Token(_ *postgresql.Client, requiredScope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		auth := c.Get("Authorization")
		tokenStr, ok := platformAuthHeader.ExtractBearerToken(auth)
		if !ok {
			c.Set("WWW-Authenticate", buildBearerChallenge("", "", ""))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "missing or invalid authorization header",
			})
		}

		introspection, err := introspectHydraToken(c.Context(), tokenStr)
		if err != nil {
			harukiLogger.Errorf("OAuth2 introspection failed: %v", err)
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status":  fiber.StatusServiceUnavailable,
				"message": "oauth2 introspection unavailable",
			})
		}

		if !introspection.Active {
			c.Set("WWW-Authenticate", buildBearerChallenge("invalid_token", "invalid or expired token", ""))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "invalid or expired token",
			})
		}

		subject := strings.TrimSpace(introspection.Subject)
		if subject == "" {
			subject = strings.TrimSpace(introspection.Username)
		}
		if subject == "" {
			c.Set("WWW-Authenticate", buildBearerChallenge("invalid_token", "token subject is missing", ""))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "token subject is missing",
			})
		}

		now := time.Now().Unix()
		if introspection.Exp > 0 && now >= introspection.Exp {
			c.Set("WWW-Authenticate", buildBearerChallenge("invalid_token", "token expired", ""))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "token expired",
			})
		}
		if introspection.Nbf > 0 && now < introspection.Nbf {
			c.Set("WWW-Authenticate", buildBearerChallenge("invalid_token", "token not active yet", ""))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "token not active yet",
			})
		}

		scopes := parseHydraScopeList(introspection.Scope)
		if requiredScope != "" && !HasScope(scopes, requiredScope) {
			c.Set("WWW-Authenticate", buildBearerChallenge("insufficient_scope", "insufficient scope", requiredScope))
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"status":  fiber.StatusForbidden,
				"message": "insufficient scope",
			})
		}

		c.Locals("userID", subject)
		c.Locals("oauth2ClientID", introspection.ClientID)
		c.Locals("oauth2Scopes", scopes)
		return c.Next()
	}
}

func VerifySessionOrOAuth2Token(sessionVerify fiber.Handler, db *postgresql.Client, requiredScope string) fiber.Handler {
	oauth2Verify := VerifyOAuth2Token(db, requiredScope)
	return func(c fiber.Ctx) error {
		auth := c.Get("Authorization")
		tokenStr, ok := platformAuthHeader.ExtractBearerToken(auth)
		if !ok {
			c.Set("WWW-Authenticate", buildBearerChallenge("", "", ""))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "missing or invalid authorization header",
			})
		}

		introspection, err := introspectHydraToken(c.Context(), tokenStr)
		if err == nil && introspection.Active {
			return oauth2Verify(c)
		}
		return sessionVerify(c)
	}
}

func parseHydraScopeList(scopeRaw string) []string {
	return strings.Fields(strings.TrimSpace(scopeRaw))
}

func introspectHydraToken(ctx context.Context, token string) (*hydraIntrospectionResponse, error) {
	targetURL, err := HydraAdminEndpoint("/admin/oauth2/introspect")
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("token", token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to build introspection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if clientID, clientSecret := HydraClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := (&http.Client{Timeout: HydraRequestTimeout()}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call hydra introspection endpoint: %w", err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read hydra introspection response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		message := http.StatusText(resp.StatusCode)
		var parsed map[string]any
		if err := json.Unmarshal(respBody, &parsed); err == nil {
			for _, key := range []string{"error_description", "message", "error"} {
				if value := strings.TrimSpace(stringifyAny(parsed[key])); value != "" {
					message = value
					break
				}
			}
		}
		return nil, &hydraIntrospectionError{Status: resp.StatusCode, Message: message}
	}

	var parsed hydraIntrospectionResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode hydra introspection payload: %w", err)
	}
	return &parsed, nil
}

func stringifyAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}
