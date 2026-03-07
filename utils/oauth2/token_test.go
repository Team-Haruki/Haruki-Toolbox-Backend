package oauth2

import (
	"encoding/hex"
	"haruki-suite/config"
	"testing"
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
