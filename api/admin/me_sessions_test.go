package admin

import (
	harukiConfig "haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestParseSessionTokenIDFromAuthorization(t *testing.T) {
	signKey := harukiConfig.Cfg.UserSystem.SessionSignToken
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

	got := parseSessionTokenIDFromAuthorization("Bearer " + signed)
	if got != "session-abc" {
		t.Fatalf("session token id = %q, want %q", got, "session-abc")
	}

	got = parseSessionTokenIDFromAuthorization("Bearer invalid.token")
	if got != "" {
		t.Fatalf("invalid token should return empty string, got %q", got)
	}

	got = parseSessionTokenIDFromAuthorization(strings.Repeat(" ", 4))
	if got != "" {
		t.Fatalf("empty header should return empty string, got %q", got)
	}
}
