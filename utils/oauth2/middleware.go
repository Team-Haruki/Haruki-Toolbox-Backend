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

		if dbToken.ExpiresAt != nil && dbToken.ExpiresAt.Before(time.Now()) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "token expired",
			})
		}

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

		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		if strings.Count(tokenStr, ".") == 2 {

			claims, err := ParseAccessToken(tokenStr)
			if err == nil && claims.ClientID != "" {

				return oauth2Verify(c)
			}
		}

		return sessionVerify(c)
	}
}
