package oauth2

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"haruki-suite/config"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type OAuth2TokenClaims struct {
	UserID   string   `json:"uid"`
	ClientID string   `json:"cid"`
	Scopes   []string `json:"scopes"`
	jwt.RegisteredClaims
}

func GenerateAccessToken(userID, clientID string, scopes []string, ttlSeconds int) (string, *time.Time, error) {
	claims := OAuth2TokenClaims{
		UserID:   userID,
		ClientID: clientID,
		Scopes:   scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	var expiresAt *time.Time
	if ttlSeconds > 0 {
		exp := time.Now().Add(time.Duration(ttlSeconds) * time.Second)
		claims.ExpiresAt = jwt.NewNumericDate(exp)
		expiresAt = &exp
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(config.Cfg.OAuth2.TokenSignKey))
	if err != nil {
		return "", nil, fmt.Errorf("failed to sign token: %w", err)
	}
	return signed, expiresAt, nil
}

func ParseAccessToken(tokenStr string) (*OAuth2TokenClaims, error) {
	parsed, err := jwt.ParseWithClaims(tokenStr, &OAuth2TokenClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(config.Cfg.OAuth2.TokenSignKey), nil
	})
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	claims, ok := parsed.Claims.(*OAuth2TokenClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}
	return claims, nil
}

func GenerateRandomToken(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func GenerateAuthorizationCode() (string, error) {
	return GenerateRandomToken(32)
}

func GenerateRefreshToken() (string, error) {
	return GenerateRandomToken(32)
}
