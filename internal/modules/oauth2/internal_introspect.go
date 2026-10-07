package oauth2

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	json "encoding/json/v2"
	"fmt"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"time"

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
// so it is reachable over the tailnet and the compose network, not publicly.
const InternalIntrospectPath = "/internal/oauth2/introspect"

// internalIntrospectMaxTokenLength bounds the token forwarded to Hydra; Hydra
// access tokens are far shorter.
const internalIntrospectMaxTokenLength = 4096

// InternalAPIConfig is the immutable configuration of the internal API. Its
// zero value is disabled: the route is not registered.
type InternalAPIConfig struct {
	tokenSHA256 []byte
}

// ParseInternalAPIConfig reads oauth2.internal_api.token_sha256: empty (after
// trimming) disables the internal API, otherwise it must be exactly 64 hex
// characters in either case.
func ParseInternalAPIConfig(tokenSHA256Hex string) (InternalAPIConfig, error) {
	value := strings.TrimSpace(tokenSHA256Hex)
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
	return InternalAPIConfig{tokenSHA256: sum}, nil
}

// Enabled reports whether the internal API is configured.
func (c InternalAPIConfig) Enabled() bool {
	return len(c.tokenSHA256) == sha256.Size
}

// authorize checks "Authorization: Bearer <internal token>" against the
// configured hash in constant time. It returns "" on success, otherwise the
// logged reason ("missing" or "mismatch").
func (c InternalAPIConfig) authorize(header string) string {
	token, ok := platformAuthHeader.ExtractBearerToken(header)
	if !ok {
		return "missing"
	}
	sum := sha256.Sum256([]byte(token))
	if !c.Enabled() || subtle.ConstantTimeCompare(sum[:], c.tokenSHA256) != 1 {
		return "mismatch"
	}
	return ""
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

// internalIntrospectionResponse is the active answer. It never carries a
// username, email or raw Hydra field.
type internalIntrospectionResponse struct {
	Active      bool   `json:"active"`
	UserID      string `json:"user_id"`
	ClientID    string `json:"client_id"`
	Scope       string `json:"scope"`
	Exp         int64  `json:"exp"`
	Iat         int64  `json:"iat"`
	DeviceLabel string `json:"device_label,omitempty"`
}

func handleInternalOAuth2Introspect(hydraConfig *harukiOAuth2.HydraConfig, cfg InternalAPIConfig, userDB func() *postgresql.Client, clientActive harukiOAuth2.ClientActiveChecker, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Revocation must apply to the next call: nothing here may be cached.
		c.Set(fiber.HeaderCacheControl, "no-store")
		c.Set(fiber.HeaderPragma, "no-cache")

		if reason := cfg.authorize(c.Get(fiber.HeaderAuthorization)); reason != "" {
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
			UserID:      result.UserID,
			ClientID:    result.ClientID,
			Scope:       strings.Join(result.Scopes, " "),
			Exp:         result.Exp,
			Iat:         result.Iat,
			DeviceLabel: result.DeviceLabel,
		})
	}
}

// parseInternalIntrospectToken reads the token from an
// application/x-www-form-urlencoded body (token=…, RFC 7662 style) or a JSON
// body {"token":"…"}. Exactly one non-empty token is required.
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
