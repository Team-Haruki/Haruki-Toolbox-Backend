package oauth2

import (
	"context"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	harukiLogger "haruki-suite/utils/logger"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// VerifyOAuth2Token is a Fiber middleware that validates an OAuth2 Bearer token.
// It sets c.Locals("userID"), c.Locals("oauth2ClientID"), and c.Locals("oauth2Scopes").
func VerifyOAuth2Token(db *postgresql.Client, requiredScope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		auth := c.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "missing or invalid authorization header",
			})
		}
		tokenStr := auth[7:]

		claims, err := ParseAccessToken(tokenStr)
		if err != nil {
			harukiLogger.Warnf("OAuth2 token parse failed: %v", err)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "invalid or expired token",
			})
		}

		// Look up the token in DB to check revocation
		dbToken, err := db.OAuthToken.Query().
			Where(
				oauthtoken.AccessTokenEQ(tokenStr),
				oauthtoken.RevokedEQ(false),
			).
			Only(context.Background())
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "token not found or revoked",
			})
		}

		// Check expiration for tokens that have one
		if dbToken.ExpiresAt != nil && dbToken.ExpiresAt.Before(time.Now()) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "token expired",
			})
		}

		// Check required scope
		if requiredScope != "" && !HasScope(claims.Scopes, requiredScope) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"status":  fiber.StatusForbidden,
				"message": "insufficient scope",
			})
		}

		c.Locals("userID", claims.UserID)
		c.Locals("oauth2ClientID", claims.ClientID)
		c.Locals("oauth2Scopes", claims.Scopes)
		return c.Next()
	}
}

// VerifySessionOrOAuth2Token tries session auth first, falls back to OAuth2.
// This allows routes to accept both session tokens and OAuth2 tokens.
func VerifySessionOrOAuth2Token(sessionVerify fiber.Handler, db *postgresql.Client, requiredScope string) fiber.Handler {
	oauth2Verify := VerifyOAuth2Token(db, requiredScope)
	return func(c fiber.Ctx) error {
		auth := c.Get("Authorization")
		if auth == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "missing authorization header",
			})
		}

		// If it looks like a JWT (3 dot-separated segments), try OAuth2 first
		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		if strings.Count(tokenStr, ".") == 2 {
			// Could be either a session JWT or OAuth2 JWT
			// Try parsing as OAuth2 token first
			claims, err := ParseAccessToken(tokenStr)
			if err == nil && claims.ClientID != "" {
				// This is an OAuth2 token (has client_id claim)
				return oauth2Verify(c)
			}
		}

		// Fall back to session token verification
		return sessionVerify(c)
	}
}
