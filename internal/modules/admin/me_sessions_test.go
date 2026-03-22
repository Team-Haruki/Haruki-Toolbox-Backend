package admin

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

func TestMapKratosSessionDeleteError(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		handler := harukiAPIHelper.NewSessionHandler(nil, "")
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
		handler := harukiAPIHelper.NewSessionHandler(nil, "")
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

func newAdminSessionTestHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *harukiRedis.HarukiRedisManager) {
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

	redisManager := &harukiRedis.HarukiRedisManager{Redis: client}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			Redis: redisManager,
		},
		SessionHandler: harukiAPIHelper.NewSessionHandler(client, ""),
	}
	return helper, redisManager
}

func TestHandleDeleteAdminSessionKratosRejectsSessionOutsideCurrentIdentity(t *testing.T) {
	helper, _ := newAdminSessionTestHelper(t)

	kratosServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/identities/kratos-admin-1/sessions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"owned-session","active":true}]`))
		case r.Method == http.MethodDelete && r.URL.Path == "/admin/sessions/foreign-session":
			t.Fatalf("foreign session should not be revoked")
		default:
			http.NotFound(w, r)
		}
	}))
	defer kratosServer.Close()

	helper.SessionHandler.ConfigureIdentityProvider("kratos", "http://kratos.example", kratosServer.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		c.Locals("identityID", "kratos-admin-1")
		return c.Next()
	})
	app.Delete("/sessions/:session_token_id", handleDeleteAdminSession(helper))

	req := httptest.NewRequest(http.MethodDelete, "/sessions/foreign-session", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
}

func TestHandleDeleteAdminSessionKratosSuccessForOwnedSession(t *testing.T) {
	helper, _ := newAdminSessionTestHelper(t)

	deleteCalled := false
	kratosServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/identities/kratos-admin-1/sessions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"owned-session","active":true}]`))
		case r.Method == http.MethodDelete && r.URL.Path == "/admin/sessions/owned-session":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer kratosServer.Close()

	helper.SessionHandler.ConfigureIdentityProvider("kratos", "http://kratos.example", kratosServer.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		c.Locals("identityID", "kratos-admin-1")
		return c.Next()
	})
	app.Delete("/sessions/:session_token_id", handleDeleteAdminSession(helper))

	req := httptest.NewRequest(http.MethodDelete, "/sessions/owned-session", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	if !deleteCalled {
		t.Fatalf("expected owned session to be revoked")
	}
}

func TestRequireRecentAdminReauth(t *testing.T) {
	helper, redisManager := newAdminSessionTestHelper(t)

	kratosPublicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sessions/whoami" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Session-Token"); got != "current-session-token" {
			t.Fatalf("X-Session-Token = %q, want %q", got, "current-session-token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"current-session-id","active":true,"identity":{"id":"kratos-admin-1","traits":{"email":"admin@example.com"}}}`))
	}))
	defer kratosPublicServer.Close()

	helper.SessionHandler.ConfigureIdentityProvider("kratos", kratosPublicServer.URL, "http://kratos-admin.example", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		c.Locals("identityID", "kratos-admin-1")
		return c.Next()
	})
	app.Put("/", RequireRecentAdminReauth(helper), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("missing reauth marker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Session-Token", "current-session-token")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("valid reauth marker", func(t *testing.T) {
		reauthKey := buildAdminReauthMarkerKey("admin-1", "kratos:current-session-id")
		if err := redisManager.Redis.Set(context.Background(), reauthKey, "1", time.Minute).Err(); err != nil {
			t.Fatalf("seed reauth marker returned error: %v", err)
		}

		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Session-Token", "current-session-token")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})
}

func TestRequireRecentAdminReauthAuthProxySession(t *testing.T) {
	helper, redisManager := newAdminSessionTestHelper(t)
	helper.SessionHandler.ConfigureIdentityProvider("kratos", "http://kratos.example", "http://kratos-admin.example", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	helper.SessionHandler.ConfigureAuthProxy(
		true,
		"X-Auth-Proxy-Secret",
		"shared-secret",
		"X-Kratos-Identity-Id",
		"X-User-Name",
		"X-User-Email",
		"X-User-Email-Verified",
		"X-User-Id",
	)
	helper.SessionHandler.ConfigureAuthProxySessionHeader("X-Auth-Proxy-Session-Id")

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		c.Locals("identityID", "kratos-admin-1")
		return c.Next()
	})
	app.Put("/", RequireRecentAdminReauth(helper), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("missing reauth marker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Auth-Proxy-Secret", "shared-secret")
		req.Header.Set("X-Kratos-Identity-Id", "kratos-admin-1")
		req.Header.Set("X-Auth-Proxy-Session-Id", "session-a")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("missing auth proxy session header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Auth-Proxy-Secret", "shared-secret")
		req.Header.Set("X-Kratos-Identity-Id", "kratos-admin-1")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("valid reauth marker", func(t *testing.T) {
		reauthKey := buildAdminReauthMarkerKey("admin-1", "authproxy-session:session-a")
		if err := redisManager.Redis.Set(context.Background(), reauthKey, "1", time.Minute).Err(); err != nil {
			t.Fatalf("seed reauth marker returned error: %v", err)
		}

		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Auth-Proxy-Secret", "shared-secret")
		req.Header.Set("X-Kratos-Identity-Id", "kratos-admin-1")
		req.Header.Set("X-Auth-Proxy-Session-Id", "session-a")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("different auth proxy session cannot reuse marker", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Auth-Proxy-Secret", "shared-secret")
		req.Header.Set("X-Kratos-Identity-Id", "kratos-admin-1")
		req.Header.Set("X-Auth-Proxy-Session-Id", "session-b")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})
}
