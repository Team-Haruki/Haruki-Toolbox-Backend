package oauth2

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"

	"github.com/gofiber/fiber/v3"
)

// AccessTokenIntrospection is what an internal service learns about an access
// token. It carries only the Toolbox user ID, never a username, email or raw
// Hydra field. When Active is false every other field is empty.
type AccessTokenIntrospection struct {
	Active   bool
	UserID   string
	ClientID string
	// Scopes are all scopes granted to the token, in Hydra's order.
	Scopes []string
	Exp    int64
	Iat    int64
	// DeviceLabel is ext.device_label, empty for tokens without one.
	DeviceLabel string
}

// IntrospectAccessToken applies the bearer middleware's rules to a token an
// internal service hands over. Every reason the middleware would answer 401
// (inactive, not an access token, expired, not yet valid, no subject, a
// subject without a local user or with a banned one, no client, a disabled or
// deleted client) yields Active false and a nil error. An error means the
// answer is unknown: Hydra, the database or the client lookup failed. The
// result is never cached, so a revocation applies to the next call.
func IntrospectAccessToken(ctx context.Context, hydraConfig *HydraConfig, db *postgresql.Client, token string, clientActiveChecker ClientActiveChecker) (AccessTokenIntrospection, error) {
	if db == nil || clientActiveChecker == nil {
		// Without them a subject would map to itself and a disabled client
		// would pass; refuse rather than answer active.
		return AccessTokenIntrospection{}, errors.New("oauth2 introspection requires the user database and a client active checker")
	}
	if strings.TrimSpace(token) == "" {
		return AccessTokenIntrospection{}, nil
	}
	result, failure := authenticateOAuth2AccessToken(ctx, hydraConfig, db, token, "", clientActiveChecker)
	if failure != nil {
		if failure.Status == fiber.StatusUnauthorized {
			return AccessTokenIntrospection{}, nil
		}
		return AccessTokenIntrospection{}, fmt.Errorf("oauth2 introspection unavailable: status %d: %s", failure.Status, failure.Message)
	}
	scopes := result.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return AccessTokenIntrospection{
		Active:      true,
		UserID:      result.UserID,
		ClientID:    result.ClientID,
		Scopes:      scopes,
		Exp:         result.Exp,
		Iat:         result.Iat,
		DeviceLabel: result.DeviceLabel,
	}, nil
}
