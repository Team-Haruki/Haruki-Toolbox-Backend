package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
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

func TestVerifySessionTokenUsesHandlerSignKey(t *testing.T) {
	redisClient := newSessionTestRedisClient(t)
	handler := NewSessionHandler(redisClient, "handler-sign-key")

	token, err := handler.IssueSession("u1")
	if err != nil {
		t.Fatalf("IssueSession returned error: %v", err)
	}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}

	reqLower := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	reqLower.Header.Set("Authorization", "bearer "+token)
	respLower, err := app.Test(reqLower)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if respLower.StatusCode != fiber.StatusNoContent {
		t.Fatalf("lowercase bearer status code = %d, want %d", respLower.StatusCode, fiber.StatusNoContent)
	}
}

func TestVerifySessionTokenFailsWhenSignKeyMissing(t *testing.T) {
	redisClient := newSessionTestRedisClient(t)
	handler := NewSessionHandler(redisClient, "")

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
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}

	reqInvalidScheme := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	reqInvalidScheme.Header.Set("Authorization", "Basic abc")
	respInvalidScheme, err := app.Test(reqInvalidScheme)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if respInvalidScheme.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("invalid scheme status code = %d, want %d", respInvalidScheme.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestVerifySessionTokenRejectsNonHS256Token(t *testing.T) {
	redisClient := newSessionTestRedisClient(t)
	handler := NewSessionHandler(redisClient, "handler-sign-key")

	if err := redisClient.Set(t.Context(), "u1:s1", "1", 7*24*time.Hour).Err(); err != nil {
		t.Fatalf("redis.Set returned error: %v", err)
	}

	claims := SessionClaims{
		UserID:       "u1",
		SessionToken: "s1",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	signed, err := token.SignedString([]byte("handler-sign-key"))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestVerifySessionTokenReturnsServiceUnavailableOnRedisError(t *testing.T) {
	handler := NewSessionHandler(redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
	}), "handler-sign-key")
	t.Cleanup(func() {
		_ = handler.RedisClient.Close()
	})

	claims := SessionClaims{
		UserID:       "u1",
		SessionToken: "s1",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte("handler-sign-key"))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
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
	if payload["message"] != "session store unavailable" {
		t.Fatalf("message = %v, want %q", payload["message"], "session store unavailable")
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
	if err := ClearUserSessionsWithContext(t.Context(), nil, "u1"); err == nil {
		t.Fatalf("expected nil redis client to fail")
	}
}

func TestVerifySessionTokenUsesTrustedAuthProxyHeaders(t *testing.T) {
	handler := NewSessionHandler(nil, "")
	handler.ConfigureAuthProxy(true, "X-Auth-Proxy-Secret", "proxy-secret", "X-Kratos-Identity-Id", "X-User-Email", "X-User-Id")

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"userID":     c.Locals("userID"),
			"identityID": c.Locals("identityID"),
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u-proxy/profile", nil)
	req.Header.Set("X-Auth-Proxy-Secret", "proxy-secret")
	req.Header.Set("X-User-Id", "u-proxy")
	req.Header.Set("X-Kratos-Identity-Id", "kratos-proxy-1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if payload["userID"] != "u-proxy" {
		t.Fatalf("userID = %q, want %q", payload["userID"], "u-proxy")
	}
	if payload["identityID"] != "kratos-proxy-1" {
		t.Fatalf("identityID = %q, want %q", payload["identityID"], "kratos-proxy-1")
	}
}
