package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestParseSessionTokenIDFromAuthorization(t *testing.T) {
	signKey := "test-sign-key"
	claims := harukiAPIHelper.SessionClaims{
		UserID:       "1001",
		SessionToken: "session-abc",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * time.Minute)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(signKey))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}

	got := parseSessionTokenIDFromAuthorization("Bearer "+signed, signKey)
	if got != "session-abc" {
		t.Fatalf("session token id = %q, want %q", got, "session-abc")
	}

	got = parseSessionTokenIDFromAuthorization("bearer "+signed, signKey)
	if got != "session-abc" {
		t.Fatalf("lowercase bearer should be accepted, got %q", got)
	}

	got = parseSessionTokenIDFromAuthorization("Bearer invalid.token", signKey)
	if got != "" {
		t.Fatalf("invalid token should return empty string, got %q", got)
	}

	got = parseSessionTokenIDFromAuthorization("Bearer "+signed, "wrong-sign-key")
	if got != "" {
		t.Fatalf("wrong sign key should return empty string, got %q", got)
	}

	got = parseSessionTokenIDFromAuthorization(strings.Repeat(" ", 4), signKey)
	if got != "" {
		t.Fatalf("empty header should return empty string, got %q", got)
	}

	got = parseSessionTokenIDFromAuthorization("Bearer "+signed, "")
	if got != "" {
		t.Fatalf("empty sign key should return empty string, got %q", got)
	}
}
