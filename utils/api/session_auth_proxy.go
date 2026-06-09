package api

import (
	"context"
	"fmt"
	platformIdentity "haruki-suite/internal/platform/identity"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

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
