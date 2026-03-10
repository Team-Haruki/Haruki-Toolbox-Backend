package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestDeriveProvisionedUserName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
		want  string
	}{
		{name: "normal email", email: "alice@example.com", want: "alice"},
		{name: "empty local part", email: "@example.com", want: "@example.com"},
		{name: "blank", email: "   ", want: "kratos-user"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := deriveProvisionedUserName(tc.email); got != tc.want {
				t.Fatalf("deriveProvisionedUserName(%q) = %q, want %q", tc.email, got, tc.want)
			}
		})
	}
}

func TestGenerateProvisionedUserIDFormat(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`^\d{10}$`)
	id, err := generateProvisionedUserID(time.Now().UTC())
	if err != nil {
		t.Fatalf("generateProvisionedUserID returned error: %v", err)
	}
	if !pattern.MatchString(id) {
		t.Fatalf("generated user id = %q, expected 10 digits", id)
	}
}

func TestVerifySessionTokenKratosMode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/whoami" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Session-Token"); got != "kratos-token" {
			http.Error(w, "missing session token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"identity":{"id":"identity-1","traits":{"email":"kratos.user@example.com"}}}`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	handler.KratosIdentityResolver = func(ctx context.Context, identityID string, email string) (string, error) {
		if identityID != "identity-1" {
			t.Fatalf("identityID = %q, want %q", identityID, "identity-1")
		}
		if email != "kratos.user@example.com" {
			t.Fatalf("email = %q, want %q", email, "kratos.user@example.com")
		}
		return "u1", nil
	}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendString(c.Locals("userID").(string))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	req.Header.Set("Authorization", "Bearer kratos-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(body) != "u1" {
		t.Fatalf("response body = %q, want %q", string(body), "u1")
	}
}

func TestVerifySessionTokenAutoModeFallsBackToKratos(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/whoami" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Session-Token"); got != "not-a-local-jwt" {
			http.Error(w, "missing session token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"identity":{"id":"identity-2","traits":{"email":"fallback@example.com"}}}`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("auto", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	handler.KratosIdentityResolver = func(ctx context.Context, identityID string, email string) (string, error) {
		if identityID != "identity-2" {
			t.Fatalf("identityID = %q, want %q", identityID, "identity-2")
		}
		return "u2", nil
	}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u2/profile", nil)
	req.Header.Set("Authorization", "Bearer not-a-local-jwt")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}

func TestVerifySessionTokenKratosModeRejectsUnmappedIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"identity":{"id":"identity-3","traits":{"email":"unmapped@example.com"}}}`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	handler.KratosIdentityResolver = func(ctx context.Context, identityID string, email string) (string, error) {
		return "", errKratosIdentityUnmapped
	}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u3/profile", nil)
	req.Header.Set("Authorization", "Bearer kratos-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if payload["message"] != "invalid user session" {
		t.Fatalf("message = %v, want %q", payload["message"], "invalid user session")
	}
}

func TestVerifySessionTokenKratosModeProviderUnavailable(t *testing.T) {
	t.Parallel()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://127.0.0.1:1", "", "X-Session-Token", "ory_kratos_session", true, true, 100*time.Millisecond, nil)
	handler.KratosHTTPClient = &http.Client{Timeout: 100 * time.Millisecond}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	req.Header.Set("Authorization", "Bearer kratos-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusServiceUnavailable)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if payload["message"] != "identity provider unavailable" {
		t.Fatalf("message = %v, want %q", payload["message"], "identity provider unavailable")
	}
}
