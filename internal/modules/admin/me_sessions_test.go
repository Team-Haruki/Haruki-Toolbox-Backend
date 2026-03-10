package admin

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http"
	"net/http/httptest"
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

func TestShouldUseKratosSessionInventory(t *testing.T) {
	localHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, "local-sign-key"),
	}
	localHelper.SessionHandler.ConfigureIdentityProvider("local", "http://kratos.example", "http://kratos-admin.example", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if shouldUseKratosSessionInventory(localHelper, "", "kratos-token", "", "") {
		t.Fatalf("local provider should not use kratos session inventory")
	}

	kratosHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, "local-sign-key"),
	}
	kratosHelper.SessionHandler.ConfigureIdentityProvider("kratos", "http://kratos.example", "http://kratos-admin.example", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if shouldUseKratosSessionInventory(kratosHelper, "local-session-id", "kratos-token", "", "bearer-token") {
		t.Fatalf("local JWT session should prefer local inventory")
	}
	if !shouldUseKratosSessionInventory(kratosHelper, "", "kratos-token", "", "") {
		t.Fatalf("kratos header token should use kratos session inventory")
	}
	if !shouldUseKratosSessionInventory(kratosHelper, "", "", "ory_kratos_session=abc", "") {
		t.Fatalf("kratos cookie should use kratos session inventory")
	}
	if !shouldUseKratosSessionInventory(kratosHelper, "", "", "", "bearer-token") {
		t.Fatalf("bearer token without local session id should use kratos inventory in kratos mode")
	}
}

func TestMapKratosSessionDeleteError(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		handler := harukiAPIHelper.NewSessionHandler(nil, "local-sign-key")
		handler.ConfigureIdentityProvider("kratos", "http://kratos.example", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

		err := handler.RevokeKratosSessionByID(context.Background(), "missing-session")
		statusCode, message, known := mapKratosSessionDeleteError(err)
		if !known {
			t.Fatalf("expected known error mapping for not found")
		}
		if statusCode != http.StatusNotFound {
			t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusNotFound)
		}
		if message != "session not found" {
			t.Fatalf("message = %q, want %q", message, "session not found")
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		handler := harukiAPIHelper.NewSessionHandler(nil, "local-sign-key")
		err := handler.RevokeKratosSessionByID(context.Background(), "")
		statusCode, message, known := mapKratosSessionDeleteError(err)
		if !known {
			t.Fatalf("expected known error mapping for invalid input")
		}
		if statusCode != http.StatusBadRequest {
			t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusBadRequest)
		}
		if message != "invalid session_token_id" {
			t.Fatalf("message = %q, want %q", message, "invalid session_token_id")
		}
	})

	t.Run("unknown", func(t *testing.T) {
		statusCode, message, known := mapKratosSessionDeleteError(context.DeadlineExceeded)
		if known {
			t.Fatalf("expected unknown error mapping")
		}
		if statusCode != http.StatusInternalServerError {
			t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusInternalServerError)
		}
		if message != "failed to delete session" {
			t.Fatalf("message = %q, want %q", message, "failed to delete session")
		}
	})
}
