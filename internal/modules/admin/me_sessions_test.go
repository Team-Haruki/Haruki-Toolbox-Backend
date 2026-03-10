package admin

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	goredis "github.com/redis/go-redis/v9"
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

func newAdminSessionTestHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, string, *harukiRedis.HarukiRedisManager) {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
	})

	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})

	signKey := "test-admin-sign-key"
	redisManager := &harukiRedis.HarukiRedisManager{Redis: client}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			Redis: redisManager,
		},
		SessionHandler: harukiAPIHelper.NewSessionHandler(client, signKey),
	}
	return helper, signKey, redisManager
}

func signAdminSessionToken(t *testing.T, signKey, userID, sessionTokenID string) string {
	t.Helper()

	claims := harukiAPIHelper.SessionClaims{
		UserID:       userID,
		SessionToken: sessionTokenID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(signKey))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}
	return signed
}

func TestHandleDeleteAdminSessionLocalNotFound(t *testing.T) {
	helper, signKey, _ := newAdminSessionTestHelper(t)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Delete("/sessions/:session_token_id", handleDeleteAdminSession(helper))

	authToken := signAdminSessionToken(t, signKey, "admin-1", "current-session")
	req := httptest.NewRequest(http.MethodDelete, "/sessions/missing-session", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
}

func TestHandleDeleteAdminSessionLocalSuccess(t *testing.T) {
	helper, signKey, redisManager := newAdminSessionTestHelper(t)

	if err := redisManager.Redis.Set(context.Background(), "admin-1:target-session", "1", time.Minute).Err(); err != nil {
		t.Fatalf("seed redis session returned error: %v", err)
	}

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Delete("/sessions/:session_token_id", handleDeleteAdminSession(helper))

	authToken := signAdminSessionToken(t, signKey, "admin-1", "current-session")
	req := httptest.NewRequest(http.MethodDelete, "/sessions/target-session", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	exists, err := redisManager.Redis.Exists(context.Background(), "admin-1:target-session").Result()
	if err != nil {
		t.Fatalf("query redis session returned error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected target session to be removed")
	}
}

func TestRequireRecentAdminReauth(t *testing.T) {
	helper, signKey, redisManager := newAdminSessionTestHelper(t)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Put("/", RequireRecentAdminReauth(helper), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	sessionTokenID := "current-session"
	authToken := signAdminSessionToken(t, signKey, "admin-1", sessionTokenID)

	t.Run("missing reauth marker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("Authorization", "Bearer "+authToken)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("valid reauth marker", func(t *testing.T) {
		reauthKey := buildAdminReauthMarkerKey("admin-1", "local:"+sessionTokenID)
		if err := redisManager.Redis.Set(context.Background(), reauthKey, "1", time.Minute).Err(); err != nil {
			t.Fatalf("seed reauth marker returned error: %v", err)
		}

		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("Authorization", "Bearer "+authToken)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})
}
