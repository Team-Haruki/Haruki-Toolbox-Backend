package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	platformIdentity "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/identity"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// AuthProxyTrustedValueMatches reports whether the provided trusted-header value
// matches the configured auth-proxy secret, using a constant-time comparison.
// This secret is the sole gate on trusted-header identity forging, so the check
// must not leak timing information about how many leading bytes matched.
func (s *SessionHandler) AuthProxyTrustedValueMatches(provided string) bool {
	expected := strings.TrimSpace(s.AuthProxyTrustedValue)
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(provided)), []byte(expected)) == 1
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
	if !s.AuthProxyTrustedValueMatches(c.Get(s.AuthProxyTrustedHeader)) {
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
		resolvedUserID, err := s.resolveKratosIdentity(ctx, identityID, email, emailVerified != nil && *emailVerified)
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
