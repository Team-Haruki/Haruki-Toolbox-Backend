package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

func newSessionTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run returned error: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
	})

	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func TestVerifySessionTokenRequiresConfiguredKratosProvider(t *testing.T) {
	handler := NewSessionHandler(newSessionTestRedisClient(t), "")

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	req.Header.Set("Authorization", "Bearer dummy-token")
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

func TestClearUserSessions(t *testing.T) {
	redisClient := newSessionTestRedisClient(t)
	if err := redisClient.Set(t.Context(), "u1:s1", "1", time.Hour).Err(); err != nil {
		t.Fatalf("redis.Set returned error: %v", err)
	}
	if err := redisClient.Set(t.Context(), "u1:s2", "1", time.Hour).Err(); err != nil {
		t.Fatalf("redis.Set returned error: %v", err)
	}
	if err := redisClient.Set(t.Context(), "u2:s1", "1", time.Hour).Err(); err != nil {
		t.Fatalf("redis.Set returned error: %v", err)
	}

	if err := ClearUserSessions(redisClient, "u1"); err != nil {
		t.Fatalf("ClearUserSessions returned error: %v", err)
	}

	remainingU1, err := redisClient.Keys(t.Context(), "u1:*").Result()
	if err != nil {
		t.Fatalf("redis.Keys returned error: %v", err)
	}
	if len(remainingU1) != 0 {
		t.Fatalf("remaining u1 keys = %v, want empty", remainingU1)
	}
	remainingU2, err := redisClient.Keys(t.Context(), "u2:*").Result()
	if err != nil {
		t.Fatalf("redis.Keys returned error: %v", err)
	}
	if len(remainingU2) != 1 {
		t.Fatalf("remaining u2 keys = %v, want 1 key", remainingU2)
	}
}

func TestClearUserSessionsWithNilRedisClient(t *testing.T) {
	if err := ClearUserSessionsWithContext(context.Background(), nil, "u1"); err == nil {
		t.Fatalf("expected nil redis client to fail")
	}
}

func TestVerifySessionTokenUsesTrustedAuthProxyHeaders(t *testing.T) {
	handler := NewSessionHandler(nil, "")
	handler.ConfigureAuthProxy(true, "X-Auth-Proxy-Secret", "proxy-secret", "X-Kratos-Identity-Id", "X-User-Name", "X-User-Email", "X-User-Email-Verified", "X-User-Id")

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"userID":        c.Locals("userID"),
			"identityID":    c.Locals("identityID"),
			"displayName":   c.Locals("displayName"),
			"emailVerified": c.Locals("emailVerified"),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u-proxy/profile", nil)
	req.Header.Set("X-Auth-Proxy-Secret", "proxy-secret")
	req.Header.Set("X-User-Id", "u-proxy")
	req.Header.Set("X-Kratos-Identity-Id", "kratos-proxy-1")
	req.Header.Set("X-User-Name", "kratos-display-name")
	req.Header.Set("X-User-Email-Verified", "true")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if payload["userID"] != "u-proxy" {
		t.Fatalf("userID = %v, want %q", payload["userID"], "u-proxy")
	}
	if payload["identityID"] != "kratos-proxy-1" {
		t.Fatalf("identityID = %v, want %q", payload["identityID"], "kratos-proxy-1")
	}
	if payload["displayName"] != "kratos-display-name" {
		t.Fatalf("displayName = %v, want %q", payload["displayName"], "kratos-display-name")
	}
	emailVerified, ok := payload["emailVerified"].(bool)
	if !ok {
		t.Fatalf("emailVerified type = %T, want bool", payload["emailVerified"])
	}
	if !emailVerified {
		t.Fatalf("emailVerified = %v, want %v", emailVerified, true)
	}
}

func TestVerifySessionTokenRequiresTrustedAuthProxyWhenEnabled(t *testing.T) {
	handler := NewSessionHandler(nil, "")
	handler.ConfigureAuthProxy(true, "X-Auth-Proxy-Secret", "proxy-secret", "X-Kratos-Identity-Id", "X-User-Name", "X-User-Email", "X-User-Email-Verified", "X-User-Id")

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
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
	if payload["message"] != "missing auth proxy identity" {
		t.Fatalf("message = %v, want %q", payload["message"], "missing auth proxy identity")
	}
}

func TestVerifySessionTokenAuthProxyIgnoresInvalidEmailVerifiedHeader(t *testing.T) {
	handler := NewSessionHandler(nil, "")
	handler.ConfigureAuthProxy(true, "X-Auth-Proxy-Secret", "proxy-secret", "X-Kratos-Identity-Id", "X-User-Name", "X-User-Email", "X-User-Email-Verified", "X-User-Id")

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"userID":        c.Locals("userID"),
			"emailVerified": c.Locals("emailVerified"),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u-proxy/profile", nil)
	req.Header.Set("X-Auth-Proxy-Secret", "proxy-secret")
	req.Header.Set("X-User-Id", "u-proxy")
	req.Header.Set("X-Kratos-Identity-Id", "kratos-proxy-1")
	req.Header.Set("X-User-Email-Verified", "maybe")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if value, exists := payload["emailVerified"]; !exists || value != nil {
		t.Fatalf("emailVerified should be nil when header is invalid, got exists=%v value=%v", exists, value)
	}
}
