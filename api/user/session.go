package user

import (
	"haruki-suite/config"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type SessionClaims struct {
	UserID       string `json:"userId"`
	SessionToken string `json:"sessionToken"`
	jwt.RegisteredClaims
}

func IssueSession(userID string) (string, error) {
	sessionToken := uuid.NewString()
	claims := SessionClaims{
		UserID:       userID,
		SessionToken: sessionToken,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(config.Cfg.UserSystem.SessionSignToken))
	if err != nil {
		return "", err
	}
	return signed, nil
}

func VerifySessionToken(c *fiber.Ctx) error {
	auth := c.Get("Authorization")
	if auth == "" {
		return UpdatedDataResponse[string](c, http.StatusUnauthorized, "missing token", nil)
	}
	tokenStr := auth
	parsed, err := jwt.ParseWithClaims(tokenStr, &SessionClaims{}, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.Cfg.UserSystem.SessionSignToken), nil
	})
	if err != nil || !parsed.Valid {
		return UpdatedDataResponse[string](c, http.StatusUnauthorized, "invalid token", nil)
	}
	claims, ok := parsed.Claims.(*SessionClaims)
	if !ok {
		return UpdatedDataResponse[string](c, http.StatusUnauthorized, "invalid claims", nil)
	}

	toolboxUserID := c.Params("toolbox_user_id")
	if toolboxUserID != "" && toolboxUserID != claims.UserID {
		return UpdatedDataResponse[string](c, http.StatusUnauthorized, "user ID mismatch", nil)
	}

	return c.Next()
}
