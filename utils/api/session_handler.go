package api

import (
	"context"
	"errors"
	"fmt"
	platformAuthHeader "haruki-suite/internal/platform/authheader"
	platformIdentity "haruki-suite/internal/platform/identity"
	"haruki-suite/utils/database/postgresql"
	harukiLogger "haruki-suite/utils/logger"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

const (
	sessionRedisOperationTimeout = 2 * time.Second
	clearUserSessionsTimeout     = 10 * time.Second

	sessionProviderKratos = "kratos"

	defaultAuthProxyTrustedHeader       = "X-Auth-Proxy-Secret"
	defaultAuthProxySubjectHeader       = "X-Kratos-Identity-Id"
	defaultAuthProxyNameHeader          = "X-User-Name"
	defaultAuthProxyEmailHeader         = "X-User-Email"
	defaultAuthProxyEmailVerifiedHeader = "X-User-Email-Verified"
	defaultAuthProxyUserIDHeader        = "X-User-Id"

	defaultKratosSessionHeader = "X-Session-Token"
	defaultKratosSessionCookie = "ory_kratos_session"
	defaultKratosTimeout       = 10 * time.Second

	kratosProvisionUserIDAttempts          = 3
	kratosProvisionUserIDTimeModulo  int64 = 10000
	kratosProvisionUserIDRandomRange int64 = 1000000
)

func normalizeSessionProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", sessionProviderKratos, "local", "auto", "hybrid":
		return sessionProviderKratos
	default:
		return sessionProviderKratos
	}
}

func NewSessionHandler(redisClient *redis.Client, _ string) *SessionHandler {
	return &SessionHandler{
		RedisClient:                  redisClient,
		SessionProvider:              sessionProviderKratos,
		AuthProxyTrustedHeader:       defaultAuthProxyTrustedHeader,
		AuthProxySubjectHeader:       defaultAuthProxySubjectHeader,
		AuthProxyNameHeader:          defaultAuthProxyNameHeader,
		AuthProxyEmailHeader:         defaultAuthProxyEmailHeader,
		AuthProxyEmailVerifiedHeader: defaultAuthProxyEmailVerifiedHeader,
		AuthProxyUserIDHeader:        defaultAuthProxyUserIDHeader,
		KratosSessionHeader:          defaultKratosSessionHeader,
		KratosSessionCookie:          defaultKratosSessionCookie,
		KratosAutoLinkByEmail:        true,
		KratosAutoProvisionUser:      true,
		KratosRequestTimeout:         defaultKratosTimeout,
		KratosIdentityResolver:       nil,
	}
}

func (s *SessionHandler) ConfigureIdentityProvider(
	provider string,
	kratosPublicURL string,
	kratosAdminURL string,
	kratosSessionHeader string,
	kratosSessionCookie string,
	kratosAutoLinkByEmail bool,
	kratosAutoProvisionUser bool,
	kratosRequestTimeout time.Duration,
	dbClient *postgresql.Client,
) {
	s.SessionProvider = normalizeSessionProvider(provider)
	s.KratosPublicURL = strings.TrimSpace(kratosPublicURL)
	s.KratosAdminURL = strings.TrimSpace(kratosAdminURL)
	s.KratosSessionHeader = strings.TrimSpace(kratosSessionHeader)
	if s.KratosSessionHeader == "" {
		s.KratosSessionHeader = defaultKratosSessionHeader
	}
	s.KratosSessionCookie = strings.TrimSpace(kratosSessionCookie)
	if s.KratosSessionCookie == "" {
		s.KratosSessionCookie = defaultKratosSessionCookie
	}
	s.KratosAutoLinkByEmail = kratosAutoLinkByEmail
	s.KratosAutoProvisionUser = kratosAutoProvisionUser
	if kratosRequestTimeout <= 0 {
		kratosRequestTimeout = defaultKratosTimeout
	}
	s.KratosRequestTimeout = kratosRequestTimeout
	s.DBClient = dbClient
	s.KratosHTTPClient = nil
}

func (s *SessionHandler) ConfigureAuthProxy(
	enabled bool,
	trustedHeader string,
	trustedValue string,
	subjectHeader string,
	nameHeader string,
	emailHeader string,
	emailVerifiedHeader string,
	userIDHeader string,
) {
	s.AuthProxyEnabled = enabled
	s.AuthProxyTrustedHeader = strings.TrimSpace(trustedHeader)
	if s.AuthProxyTrustedHeader == "" {
		s.AuthProxyTrustedHeader = defaultAuthProxyTrustedHeader
	}
	s.AuthProxyTrustedValue = strings.TrimSpace(trustedValue)
	s.AuthProxySubjectHeader = strings.TrimSpace(subjectHeader)
	if s.AuthProxySubjectHeader == "" {
		s.AuthProxySubjectHeader = defaultAuthProxySubjectHeader
	}
	s.AuthProxyNameHeader = strings.TrimSpace(nameHeader)
	if s.AuthProxyNameHeader == "" {
		s.AuthProxyNameHeader = defaultAuthProxyNameHeader
	}
	s.AuthProxyEmailHeader = strings.TrimSpace(emailHeader)
	if s.AuthProxyEmailHeader == "" {
		s.AuthProxyEmailHeader = defaultAuthProxyEmailHeader
	}
	s.AuthProxyEmailVerifiedHeader = strings.TrimSpace(emailVerifiedHeader)
	if s.AuthProxyEmailVerifiedHeader == "" {
		s.AuthProxyEmailVerifiedHeader = defaultAuthProxyEmailVerifiedHeader
	}
	s.AuthProxyUserIDHeader = strings.TrimSpace(userIDHeader)
	if s.AuthProxyUserIDHeader == "" {
		s.AuthProxyUserIDHeader = defaultAuthProxyUserIDHeader
	}
}

func (s *SessionHandler) ConfigureAuthProxySessionHeader(sessionHeader string) {
	if s == nil {
		return
	}
	s.AuthProxySessionHeader = strings.TrimSpace(sessionHeader)
}

func (s *SessionHandler) UsesAuthProxy() bool {
	return s != nil && s.AuthProxyEnabled && strings.TrimSpace(s.AuthProxyTrustedHeader) != "" && strings.TrimSpace(s.AuthProxyTrustedValue) != ""
}

func (s *SessionHandler) UsesManagedBrowserAuth() bool {
	return s != nil && (s.UsesKratosProvider() || s.UsesAuthProxy())
}

func (s *SessionHandler) hasKratosProviderConfigured() bool {
	return strings.TrimSpace(s.KratosPublicURL) != ""
}

func (s *SessionHandler) kratosHTTPClient() *http.Client {
	if s.KratosHTTPClient != nil {
		return s.KratosHTTPClient
	}
	timeout := s.KratosRequestTimeout
	if timeout <= 0 {
		timeout = defaultKratosTimeout
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &kratosRequestMetadataTransport{
			base: http.DefaultTransport,
		},
	}
}

func (s *SessionHandler) UsesKratosProvider() bool {
	provider := normalizeSessionProvider(s.SessionProvider)
	return provider == sessionProviderKratos && s.hasKratosProviderConfigured()
}

func parseAuthProxyBooleanHeader(raw string) *bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func (s *SessionHandler) verifyAuthProxySession(ctx context.Context, c fiber.Ctx) (string, string, *string, *bool, bool, error) {
	if !s.UsesAuthProxy() {
		return "", "", nil, nil, false, nil
	}
	if strings.TrimSpace(c.Get(s.AuthProxyTrustedHeader)) != strings.TrimSpace(s.AuthProxyTrustedValue) {
		return "", "", nil, nil, false, nil
	}

	identityID := strings.TrimSpace(c.Get(s.AuthProxySubjectHeader))
	displayName := strings.TrimSpace(c.Get(s.AuthProxyNameHeader))
	var displayNamePtr *string
	if displayName != "" {
		displayNamePtr = &displayName
	}
	email := platformIdentity.NormalizeEmail(c.Get(s.AuthProxyEmailHeader))
	emailVerified := parseAuthProxyBooleanHeader(c.Get(s.AuthProxyEmailVerifiedHeader))
	userID := strings.TrimSpace(c.Get(s.AuthProxyUserIDHeader))
	if userID == "" {
		if identityID == "" {
			return "", "", displayNamePtr, emailVerified, true, fmt.Errorf("%w: missing auth proxy subject header", errSessionUnauthorized)
		}
		resolvedUserID, err := s.resolveKratosIdentity(ctx, identityID, email)
		if err != nil {
			return "", "", displayNamePtr, emailVerified, true, err
		}
		userID = resolvedUserID
	}
	if identityID == "" {
		return "", "", displayNamePtr, emailVerified, true, nil
	}
	s.syncResolvedUserProfile(ctx, userID, identityID, email, displayNamePtr)
	return userID, identityID, displayNamePtr, emailVerified, true, nil
}

func (s *SessionHandler) VerifySessionToken(c fiber.Ctx) error {
	authHeader := c.Get("Authorization")
	bearerToken, hasBearerToken := platformAuthHeader.ExtractBearerToken(authHeader)
	kratosHeaderToken := strings.TrimSpace(c.Get(s.KratosSessionHeader))
	cookieHeader := strings.TrimSpace(c.Get("Cookie"))

	applyResolvedUserIdentity := func(userID string, identityID string, displayName *string, emailVerified *bool) error {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
		}
		toolboxUserID := strings.TrimSpace(c.Params("toolbox_user_id"))
		if toolboxUserID != "" && toolboxUserID != userID {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "user ID mismatch", nil)
		}
		c.Locals("userID", userID)
		if trimmedIdentityID := strings.TrimSpace(identityID); trimmedIdentityID != "" {
			c.Locals("identityID", trimmedIdentityID)
		}
		if displayName != nil && strings.TrimSpace(*displayName) != "" {
			c.Locals("displayName", strings.TrimSpace(*displayName))
		}
		if emailVerified != nil {
			c.Locals("emailVerified", *emailVerified)
		}
		return c.Next()
	}

	requestCtx := WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP())
	if proxyUserID, proxyIdentityID, proxyDisplayName, proxyEmailVerified, handled, err := s.verifyAuthProxySession(requestCtx, c); handled {
		if err != nil {
			return respondSessionVerifyError(c, err)
		}
		if sessionHeader := strings.TrimSpace(s.AuthProxySessionHeader); sessionHeader != "" {
			if sessionID := strings.TrimSpace(c.Get(sessionHeader)); sessionID != "" {
				c.Locals("authProxySessionID", sessionID)
			}
		}
		return applyResolvedUserIdentity(proxyUserID, proxyIdentityID, proxyDisplayName, proxyEmailVerified)
	}
	if s.UsesAuthProxy() {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing auth proxy identity", nil)
	}

	if !hasBearerToken && kratosHeaderToken == "" && cookieHeader == "" {
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "missing token", nil)
	}

	if !s.hasKratosProviderConfigured() {
		return respondSessionVerifyError(c, fmt.Errorf("%w: kratos public url is not configured", errIdentityProviderUnavailable))
	}
	kratosToken := firstNonEmpty(kratosHeaderToken, bearerToken)
	resolved, err := s.resolveKratosSession(requestCtx, kratosToken, cookieHeader)
	if err != nil {
		return respondSessionVerifyError(c, err)
	}
	return applyResolvedUserIdentity(resolved.UserID, resolved.IdentityID, resolved.DisplayName, resolved.EmailVerified)
}

func respondSessionVerifyError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, errSessionStoreUnavailable):
		harukiLogger.Errorf("Session store unavailable: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusServiceUnavailable, "session store unavailable", nil)
	case errors.Is(err, errIdentityProviderUnavailable):
		harukiLogger.Errorf("Identity provider unavailable: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusServiceUnavailable, "identity provider unavailable", nil)
	case errors.Is(err, errUserStoreUnavailable):
		harukiLogger.Errorf("User store unavailable: %v", err)
		return UpdatedDataResponse[string](c, fiber.StatusServiceUnavailable, "user store unavailable", nil)
	case errors.Is(err, errKratosIdentityUnmapped):
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid user session", nil)
	default:
		return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid token", nil)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
