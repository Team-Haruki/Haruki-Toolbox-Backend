package oauth2

import (
	"encoding/hex"
	"haruki-suite/config"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateAndParseAccessToken(t *testing.T) {
	original := config.Cfg.OAuth2.TokenSignKey
	config.Cfg.OAuth2.TokenSignKey = "unit-test-sign-key"
	defer func() {
		config.Cfg.OAuth2.TokenSignKey = original
	}()

	token, expiresAt, err := GenerateAccessToken("user-1", "client-1", []string{"user:read"}, 3600)
	if err != nil {
		t.Fatalf("GenerateAccessToken returned error: %v", err)
	}
	if expiresAt == nil {
		t.Fatalf("expiresAt should not be nil when ttlSeconds > 0")
	}

	claims, err := ParseAccessToken(token)
	if err != nil {
		t.Fatalf("ParseAccessToken returned error: %v", err)
	}
	if claims.UserID != "user-1" {
		t.Fatalf("claims.UserID = %q, want %q", claims.UserID, "user-1")
	}
	if claims.ClientID != "client-1" {
		t.Fatalf("claims.ClientID = %q, want %q", claims.ClientID, "client-1")
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != "user:read" {
		t.Fatalf("claims.Scopes = %#v, want [\"user:read\"]", claims.Scopes)
	}
	if claims.ExpiresAt == nil {
		t.Fatalf("claims.ExpiresAt should not be nil")
	}
}

func TestGenerateAccessTokenWithoutExpiry(t *testing.T) {
	original := config.Cfg.OAuth2.TokenSignKey
	config.Cfg.OAuth2.TokenSignKey = "unit-test-sign-key"
	defer func() {
		config.Cfg.OAuth2.TokenSignKey = original
	}()

	token, expiresAt, err := GenerateAccessToken("user-1", "client-1", []string{"user:read"}, 0)
	if err != nil {
		t.Fatalf("GenerateAccessToken returned error: %v", err)
	}
	if expiresAt != nil {
		t.Fatalf("expiresAt should be nil when ttlSeconds <= 0")
	}

	claims, err := ParseAccessToken(token)
	if err != nil {
		t.Fatalf("ParseAccessToken returned error: %v", err)
	}
	if claims.ExpiresAt != nil {
		t.Fatalf("claims.ExpiresAt should be nil when ttlSeconds <= 0")
	}
}

func TestParseAccessTokenWithWrongSignKeyFails(t *testing.T) {
	original := config.Cfg.OAuth2.TokenSignKey
	defer func() {
		config.Cfg.OAuth2.TokenSignKey = original
	}()

	config.Cfg.OAuth2.TokenSignKey = "key-1"
	token, _, err := GenerateAccessToken("user-1", "client-1", []string{"user:read"}, 3600)
	if err != nil {
		t.Fatalf("GenerateAccessToken returned error: %v", err)
	}

	config.Cfg.OAuth2.TokenSignKey = "key-2"
	if _, err := ParseAccessToken(token); err == nil {
		t.Fatalf("ParseAccessToken should fail when sign key changed")
	}
}

func TestAccessTokenRequiresSignKey(t *testing.T) {
	original := config.Cfg.OAuth2.TokenSignKey
	defer func() {
		config.Cfg.OAuth2.TokenSignKey = original
	}()

	config.Cfg.OAuth2.TokenSignKey = "   "

	if _, _, err := GenerateAccessToken("user-1", "client-1", []string{"user:read"}, 3600); err == nil {
		t.Fatalf("GenerateAccessToken should fail when sign key is empty")
	}

	if _, err := ParseAccessToken("invalid-token"); err == nil {
		t.Fatalf("ParseAccessToken should fail when sign key is empty")
	} else if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenerateRandomTokenLengthAndFormat(t *testing.T) {
	token, err := GenerateRandomToken(32)
	if err != nil {
		t.Fatalf("GenerateRandomToken returned error: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token should be valid hex string: %v", err)
	}
}

func TestGenerateAuthorizationAndRefreshTokenLength(t *testing.T) {
	authCode, err := GenerateAuthorizationCode()
	if err != nil {
		t.Fatalf("GenerateAuthorizationCode returned error: %v", err)
	}
	refreshToken, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken returned error: %v", err)
	}
	if len(authCode) != 64 {
		t.Fatalf("authorization code length = %d, want 64", len(authCode))
	}
	if len(refreshToken) != 64 {
		t.Fatalf("refresh token length = %d, want 64", len(refreshToken))
	}
}

func TestParseAccessTokenRejectsNonHS256Token(t *testing.T) {
	original := config.Cfg.OAuth2.TokenSignKey
	config.Cfg.OAuth2.TokenSignKey = "unit-test-sign-key"
	defer func() {
		config.Cfg.OAuth2.TokenSignKey = original
	}()

	claims := OAuth2TokenClaims{
		UserID:   "user-1",
		ClientID: "client-1",
		Scopes:   []string{"user:read"},
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	signed, err := token.SignedString([]byte(config.Cfg.OAuth2.TokenSignKey))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}

	if _, err := ParseAccessToken(signed); err == nil {
		t.Fatalf("ParseAccessToken should reject non-HS256 tokens")
	}
}
