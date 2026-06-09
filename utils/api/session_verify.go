package api

import (
	"errors"
	"fmt"
	platformAuthHeader "haruki-suite/internal/platform/authheader"
	harukiLogger "haruki-suite/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
)

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
