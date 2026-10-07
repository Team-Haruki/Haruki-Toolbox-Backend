package oauth2

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	json "encoding/json/v2"
	"fmt"
	"mime"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformAuthHeader "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/authheader"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/redact"

	"github.com/gofiber/fiber/v3"
)

// InternalIntrospectPath is served on the backend port only. Oathkeeper has no
// rule for /internal/ (architecture test TestInternalAPINotRoutedByOathkeeper),
// so it is reachable only on the private network / compose network, never via
// the public proxy.
const InternalIntrospectPath = "/internal/oauth2/introspect"

// internalIntrospectMaxTokenLength bounds the token forwarded to Hydra; Hydra
// access tokens are far shorter.
const internalIntrospectMaxTokenLength = 4096

// internalIntrospectTokenType is the RFC 7662 token_type of every active
// answer: only access tokens are ever active, and Hydra issues them as
// RFC 6750 bearer tokens.
const internalIntrospectTokenType = "Bearer"

// InternalAPISettings is oauth2.internal_api as read from config, after the
// config package has filled in the defaults of client_id and audience.
type InternalAPISettings struct {
	// TokenSHA256 is the hex SHA-256 of the internal token; empty disables
	// the internal API.
	TokenSHA256 string
	// ClientID is the only HTTP Basic username accepted.
	ClientID string
	// Audience is returned as "aud" in every active answer.
	Audience []string
}

// InternalAPIConfig is the immutable configuration of the internal API. Its
// zero value is disabled: the route is not registered.
type InternalAPIConfig struct {
	tokenSHA256    []byte
	clientIDSHA256 []byte
	audience       []string
}

// ParseInternalAPIConfig validates oauth2.internal_api. An empty
// token_sha256 (after trimming) disables the internal API and nothing else is
// checked. Otherwise token_sha256 must be exactly 64 hex characters in either
// case, client_id must be a non-empty HTTP Basic user-id (no colon, no
// control characters, no surrounding whitespace) and audience must hold at
// least one entry, none of them empty or padded with whitespace. Errors name
// the key but never repeat its value.
func ParseInternalAPIConfig(settings InternalAPISettings) (InternalAPIConfig, error) {
	value := strings.TrimSpace(settings.TokenSHA256)
	if value == "" {
		return InternalAPIConfig{}, nil
	}
	if len(value) != 2*sha256.Size {
		return InternalAPIConfig{}, fmt.Errorf("oauth2.internal_api.token_sha256 must be %d hex characters, got %d", 2*sha256.Size, len(value))
	}
	sum, err := hex.DecodeString(value)
	if err != nil {
		return InternalAPIConfig{}, fmt.Errorf("oauth2.internal_api.token_sha256 must be hex")
	}
	if !validInternalAPIConfigString(settings.ClientID) || strings.Contains(settings.ClientID, ":") {
		return InternalAPIConfig{}, fmt.Errorf("oauth2.internal_api.client_id must be non-empty, without a colon, control characters or surrounding whitespace")
	}
	if len(settings.Audience) == 0 {
		return InternalAPIConfig{}, fmt.Errorf("oauth2.internal_api.audience must list at least one value")
	}
	for _, entry := range settings.Audience {
		if !validInternalAPIConfigString(entry) {
			return InternalAPIConfig{}, fmt.Errorf("oauth2.internal_api.audience entries must be non-empty, without control characters or surrounding whitespace")
		}
	}
	clientIDSum := sha256.Sum256([]byte(settings.ClientID))
	return InternalAPIConfig{
		tokenSHA256:    sum,
		clientIDSHA256: clientIDSum[:],
		audience:       slices.Clone(settings.Audience),
	}, nil
}

func validInternalAPIConfigString(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

// Enabled reports whether the internal API is configured.
func (c InternalAPIConfig) Enabled() bool {
	return len(c.tokenSHA256) == sha256.Size
}

// authorize checks the caller's credentials against the configured hash in
// constant time. Exactly one Authorization header is accepted, either
// "Bearer <internal token>" or RFC 7617 "Basic base64(client_id:internal
// token)" as an RFC 7662 client sends it (RFC 6749 §2.3.1). It returns "" on
// success, otherwise the logged reason: "missing", "multiple" (more than one
// Authorization header, e.g. Bearer and Basic together), "unsupported_scheme",
// "malformed", "client_mismatch" or "mismatch". No reason carries a
// credential.
func (c InternalAPIConfig) authorize(headers [][]byte) string {
	switch len(headers) {
	case 0:
		return "missing"
	case 1:
	default:
		return "multiple"
	}
	header := string(headers[0])
	fields := strings.Fields(header)
	if len(fields) == 0 {
		return "missing"
	}
	switch scheme := fields[0]; {
	case strings.EqualFold(scheme, "Bearer"):
		token, ok := platformAuthHeader.ExtractBearerToken(header)
		if !ok {
			return "malformed"
		}
		if !c.Enabled() || !c.secretMatches(token) {
			return "mismatch"
		}
		return ""
	case strings.EqualFold(scheme, "Basic"):
		if len(fields) != 2 {
			return "malformed"
		}
		clientID, secret, ok := parseInternalBasicCredentials(fields[1])
		if !ok {
			return "malformed"
		}
		if !c.Enabled() {
			return "mismatch"
		}
		// Both comparisons always run, so the timing does not reveal which
		// half was wrong.
		clientSum := sha256.Sum256([]byte(clientID))
		clientOK := subtle.ConstantTimeCompare(clientSum[:], c.clientIDSHA256) == 1
		secretOK := c.secretMatches(secret)
		switch {
		case !clientOK:
			return "client_mismatch"
		case !secretOK:
			return "mismatch"
		}
		return ""
	default:
		return "unsupported_scheme"
	}
}

// secretMatches compares the SHA-256 of secret with the configured hash in
// constant time.
func (c InternalAPIConfig) secretMatches(secret string) bool {
	sum := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(sum[:], c.tokenSHA256) == 1
}

// parseInternalBasicCredentials decodes the token68 of a Basic header:
// standard base64 of "user-id:password", both halves non-empty. Like
// net/http's Request.BasicAuth (which Station's client mirrors with
// SetBasicAuth), the halves are taken literally, not form-urldecoded; the
// configured client_id and the hex internal token are identical either way.
func parseInternalBasicCredentials(credentials string) (string, string, bool) {
	decoded, err := base64.StdEncoding.DecodeString(credentials)
	if err != nil {
		return "", "", false
	}
	clientID, secret, ok := strings.Cut(string(decoded), ":")
	if !ok || clientID == "" || secret == "" {
		return "", "", false
	}
	return clientID, secret, true
}

// InternalRouteOptions configures RegisterOAuth2InternalRoutes.
type InternalRouteOptions struct {
	HydraConfig *harukiOAuth2.HydraConfig
	Config      InternalAPIConfig
	// Logger defaults to the global logger named OAuth2InternalAPI.
	Logger *harukiLogger.Logger
}

// RegisterOAuth2InternalRoutes registers POST /internal/oauth2/introspect on
// the main app when the internal API is configured; otherwise nothing is
// registered.
func RegisterOAuth2InternalRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, options InternalRouteOptions) {
	if apiHelper == nil || apiHelper.Router == nil || !options.Config.Enabled() {
		return
	}
	logger := options.Logger
	if logger == nil {
		logger = harukiLogger.NewLoggerFromGlobal("OAuth2InternalAPI")
	}
	userDB := func() *postgresql.Client {
		if apiHelper.DBManager == nil {
			return nil
		}
		return apiHelper.DBManager.DB
	}
	apiHelper.Router.Post(InternalIntrospectPath, handleInternalOAuth2Introspect(options.HydraConfig, options.Config, userDB, cachedClientActiveChecker(options.HydraConfig), logger))
}

// cachedClientActiveChecker is checkHydraOAuth2ClientActive behind the 5 s
// in-process client cache the device flow uses: a disabled or deleted client
// stops validating within 5 s. Lookup errors are not cached.
func cachedClientActiveChecker(hydraConfig *harukiOAuth2.HydraConfig) harukiOAuth2.ClientActiveChecker {
	cache := newDeviceClientCache(time.Now)
	lookup := func(ctx context.Context, clientID string) (*HydraOAuthClient, error) {
		return GetHydraOAuthClient(ctx, hydraConfig, clientID)
	}
	return func(ctx context.Context, clientID string) (bool, error) {
		client, err := cache.get(ctx, lookup, clientID)
		if err != nil {
			return false, err
		}
		return client != nil && HydraOAuthClientActive(client), nil
	}
}

// internalIntrospectionResponse is the active answer, shaped for an RFC 7662
// client. It never carries a username, email or raw Hydra field: sub is the
// local users.id (the same value as user_id), not Hydra's subject, and aud is
// the configured oauth2.internal_api.audience. The endpoint vouches for the
// token to its single internal caller, so aud names that caller rather than
// echoing the audience Hydra granted (usually empty). iss is omitted: an
// RFC 7662 client checks it only when present.
type internalIntrospectionResponse struct {
	Active      bool     `json:"active"`
	Sub         string   `json:"sub"`
	UserID      string   `json:"user_id"`
	ClientID    string   `json:"client_id"`
	Aud         []string `json:"aud"`
	Scope       string   `json:"scope"`
	TokenType   string   `json:"token_type"`
	Exp         int64    `json:"exp"`
	Iat         int64    `json:"iat"`
	DeviceLabel string   `json:"device_label,omitempty"`
}

func handleInternalOAuth2Introspect(hydraConfig *harukiOAuth2.HydraConfig, cfg InternalAPIConfig, userDB func() *postgresql.Client, clientActive harukiOAuth2.ClientActiveChecker, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Revocation must apply to the next call: nothing here may be cached.
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Set(fiber.HeaderPragma, "no-cache")

		if reason := cfg.authorize(c.Request().Header.PeekAll(fiber.HeaderAuthorization)); reason != "" {
			logger.Warnf("oauth2_internal event=unauthorized path=%s reason=%s ip=%s", InternalIntrospectPath, reason, strconv.Quote(c.IP()))
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}

		token, ok := parseInternalIntrospectToken(c)
		if !ok {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_request"})
		}

		result, err := harukiOAuth2.IntrospectAccessToken(c.Context(), hydraConfig, userDB(), token, clientActive)
		if err != nil {
			logger.Errorf("oauth2_internal event=introspect_failed error=%s", strconv.Quote(redact.Error(err, token).Error()))
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "temporarily_unavailable"})
		}
		if !result.Active {
			return c.JSON(fiber.Map{"active": false})
		}
		return c.JSON(internalIntrospectionResponse{
			Active:      true,
			Sub:         result.UserID,
			UserID:      result.UserID,
			ClientID:    result.ClientID,
			Aud:         cfg.audience,
			Scope:       strings.Join(result.Scopes, " "),
			TokenType:   internalIntrospectTokenType,
			Exp:         result.Exp,
			Iat:         result.Iat,
			DeviceLabel: result.DeviceLabel,
		})
	}
}

// parseInternalIntrospectToken reads the token from an
// application/x-www-form-urlencoded body (token=…, RFC 7662 style) or a JSON
// body {"token":"…"}. Exactly one non-empty token is required. token_type_hint
// (and any other parameter) is ignored: RFC 7662 §2.1 lets the server ignore
// the hint and requires it to search every token type anyway, and the answer
// does not depend on it, because only an access token can ever be active.
func parseInternalIntrospectToken(c fiber.Ctx) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	if err != nil {
		return "", false
	}
	var token string
	switch mediaType {
	case fiber.MIMEApplicationForm:
		form, err := url.ParseQuery(string(c.Body()))
		if err != nil || len(form["token"]) != 1 {
			return "", false
		}
		token = form.Get("token")
	case fiber.MIMEApplicationJSON:
		var body struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return "", false
		}
		token = body.Token
	default:
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > internalIntrospectMaxTokenLength {
		return "", false
	}
	return token, true
}
