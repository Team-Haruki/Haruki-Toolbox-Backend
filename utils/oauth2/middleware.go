package oauth2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	platformAuthHeader "haruki-suite/internal/platform/authheader"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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

type oauth2BearerAuthResult struct {
	Subject  string
	ClientID string
	Scopes   []string
}

type oauth2BearerAuthFailure struct {
	Status    int
	ErrorCode string
	Message   string
	Scope     string
}

var (
	hydraIntrospectionHTTPClientMu      sync.RWMutex
	hydraIntrospectionSharedHTTPClient  *http.Client
	hydraIntrospectionSharedTimeoutNano int64
)

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

func VerifyOAuth2Token(db *postgresql.Client, requiredScope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		result, authFailure := authenticateOAuth2BearerToken(c, db, requiredScope)
		if authFailure != nil {
			return respondOAuth2BearerError(c, authFailure)
		}
		applyOAuth2BearerAuthLocals(c, result)
		return c.Next()
	}
}

func VerifySessionOrOAuth2Token(sessionVerify fiber.Handler, db *postgresql.Client, requiredScope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		auth := strings.TrimSpace(c.Get("Authorization"))
		if auth == "" {
			return sessionVerify(c)
		}
		if _, ok := platformAuthHeader.ExtractBearerToken(auth); !ok {
			return sessionVerify(c)
		}

		result, authFailure := authenticateOAuth2BearerToken(c, db, requiredScope)
		if authFailure != nil {
			return respondOAuth2BearerError(c, authFailure)
		}
		applyOAuth2BearerAuthLocals(c, result)
		return c.Next()
	}
}

func respondOAuth2BearerError(c fiber.Ctx, failure *oauth2BearerAuthFailure) error {
	if failure == nil {
		return nil
	}
	if failure.Status == fiber.StatusUnauthorized || failure.ErrorCode != "" || failure.Scope != "" {
		c.Set("WWW-Authenticate", buildBearerChallenge(failure.ErrorCode, failure.Message, failure.Scope))
	}
	return c.Status(failure.Status).JSON(fiber.Map{
		"status":  failure.Status,
		"message": failure.Message,
	})
}

func applyOAuth2BearerAuthLocals(c fiber.Ctx, result *oauth2BearerAuthResult) {
	if result == nil {
		return
	}
	c.Locals("userID", result.Subject)
	c.Locals("oauth2ClientID", result.ClientID)
	c.Locals("oauth2Scopes", result.Scopes)
}

func authenticateOAuth2BearerToken(c fiber.Ctx, db *postgresql.Client, requiredScope string) (*oauth2BearerAuthResult, *oauth2BearerAuthFailure) {
	tokenStr, ok := platformAuthHeader.ExtractBearerToken(c.Get("Authorization"))
	if !ok {
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusUnauthorized, Message: "missing or invalid authorization header"}
	}

	introspection, err := introspectHydraToken(c.Context(), tokenStr)
	if err != nil {
		harukiLogger.Errorf("OAuth2 introspection failed: %v", err)
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusServiceUnavailable, Message: "oauth2 introspection unavailable"}
	}
	if !introspection.Active {
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusUnauthorized, ErrorCode: "invalid_token", Message: "invalid or expired token"}
	}

	subject := strings.TrimSpace(introspection.Subject)
	if subject == "" {
		subject = strings.TrimSpace(introspection.Username)
	}
	if subject == "" {
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusUnauthorized, ErrorCode: "invalid_token", Message: "token subject is missing"}
	}

	now := time.Now().Unix()
	if introspection.Exp > 0 && now >= introspection.Exp {
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusUnauthorized, ErrorCode: "invalid_token", Message: "token expired"}
	}
	if introspection.Nbf > 0 && now < introspection.Nbf {
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusUnauthorized, ErrorCode: "invalid_token", Message: "token not active yet"}
	}

	if subjectErr := ensureOAuth2BearerSubjectActive(c.Context(), db, subject); subjectErr != nil {
		if fErr, ok := subjectErr.(*fiber.Error); ok {
			if fErr.Code == fiber.StatusUnauthorized {
				return nil, &oauth2BearerAuthFailure{Status: fiber.StatusUnauthorized, ErrorCode: "invalid_token", Message: fErr.Message}
			}
			return nil, &oauth2BearerAuthFailure{Status: fErr.Code, Message: fErr.Message}
		}
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusServiceUnavailable, Message: "oauth2 subject validation unavailable"}
	}

	scopes := parseHydraScopeList(introspection.Scope)
	if requiredScope != "" && !HasScope(scopes, requiredScope) {
		return nil, &oauth2BearerAuthFailure{Status: fiber.StatusForbidden, ErrorCode: "insufficient_scope", Message: "insufficient scope", Scope: requiredScope}
	}

	return &oauth2BearerAuthResult{
		Subject:  subject,
		ClientID: introspection.ClientID,
		Scopes:   scopes,
	}, nil
}

func ensureOAuth2BearerSubjectActive(ctx context.Context, db *postgresql.Client, subject string) error {
	if db == nil {
		return nil
	}
	dbUser, err := db.User.Query().
		Where(userSchema.IDEQ(strings.TrimSpace(subject))).
		Select(userSchema.FieldBanned).
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return fiber.NewError(fiber.StatusUnauthorized, "token subject is invalid")
		}
		harukiLogger.Errorf("OAuth2 subject validation failed: %v", err)
		return fiber.NewError(fiber.StatusServiceUnavailable, "oauth2 subject validation unavailable")
	}
	if dbUser.Banned {
		return fiber.NewError(fiber.StatusUnauthorized, "token subject is invalid")
	}
	return nil
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

	resp, err := hydraIntrospectionHTTPClient().Do(req)
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

func hydraIntrospectionHTTPClient() *http.Client {
	timeout := HydraRequestTimeout()
	timeoutNano := timeout.Nanoseconds()

	hydraIntrospectionHTTPClientMu.RLock()
	if hydraIntrospectionSharedHTTPClient != nil && hydraIntrospectionSharedTimeoutNano == timeoutNano {
		client := hydraIntrospectionSharedHTTPClient
		hydraIntrospectionHTTPClientMu.RUnlock()
		return client
	}
	hydraIntrospectionHTTPClientMu.RUnlock()

	client := &http.Client{Timeout: timeout}

	hydraIntrospectionHTTPClientMu.Lock()
	defer hydraIntrospectionHTTPClientMu.Unlock()
	if hydraIntrospectionSharedHTTPClient == nil || hydraIntrospectionSharedTimeoutNano != timeoutNano {
		hydraIntrospectionSharedHTTPClient = client
		hydraIntrospectionSharedTimeoutNano = timeoutNano
	}
	return hydraIntrospectionSharedHTTPClient
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
