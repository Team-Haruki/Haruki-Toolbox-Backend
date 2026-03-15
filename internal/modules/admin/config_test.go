package admin

import (
	"bytes"
	"encoding/json"
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

func TestSanitizePublicAPIAllowedKeys(t *testing.T) {
	t.Run("trim and dedupe", func(t *testing.T) {
		keys, err := sanitizePublicAPIAllowedKeys([]string{" foo ", "foo", "bar"})
		if err != nil {
			t.Fatalf("sanitizePublicAPIAllowedKeys returned error: %v", err)
		}
		if len(keys) != 2 {
			t.Fatalf("len(keys) = %d, want %d", len(keys), 2)
		}
		if keys[0] != "foo" || keys[1] != "bar" {
			t.Fatalf("unexpected keys: %#v", keys)
		}
	})

	t.Run("empty key rejected", func(t *testing.T) {
		if _, err := sanitizePublicAPIAllowedKeys([]string{"ok", ""}); err == nil {
			t.Fatalf("expected error for empty key")
		}
	})
}

func TestHandleUpdatePublicAPIAllowedKeys(t *testing.T) {
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	helper.SetPublicAPIAllowedKeys([]string{"initial"})

	app := fiber.New()
	app.Put("/", handleUpdatePublicAPIAllowedKeys(helper))

	payload := map[string]any{
		"publicApiAllowedKeys": []string{" key-a ", "key-b", "key-a"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	updatedKeys := helper.GetPublicAPIAllowedKeys()
	if len(updatedKeys) != 2 || updatedKeys[0] != "key-a" || updatedKeys[1] != "key-b" {
		t.Fatalf("helper keys not updated as expected: %#v", updatedKeys)
	}
}

func TestHandleUpdatePublicAPIAllowedKeysRejectsEmpty(t *testing.T) {
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	app := fiber.New()
	app.Put("/", handleUpdatePublicAPIAllowedKeys(helper))

	payload := map[string]any{
		"publicApiAllowedKeys": []string{"valid", "   "},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

func TestSanitizeOptionalRuntimeSecret(t *testing.T) {
	t.Run("nil accepted", func(t *testing.T) {
		got, err := sanitizeOptionalRuntimeSecret(nil, "token")
		if err != nil {
			t.Fatalf("sanitizeOptionalRuntimeSecret returned error: %v", err)
		}
		if got != nil {
			t.Fatalf("got = %#v, want nil", got)
		}
	})

	t.Run("trim value", func(t *testing.T) {
		raw := "  abc  "
		got, err := sanitizeOptionalRuntimeSecret(&raw, "token")
		if err != nil {
			t.Fatalf("sanitizeOptionalRuntimeSecret returned error: %v", err)
		}
		if got == nil || *got != "abc" {
			t.Fatalf("got = %#v, want abc", got)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		raw := "   "
		if _, err := sanitizeOptionalRuntimeSecret(&raw, "token"); err == nil {
			t.Fatalf("expected error for empty secret")
		}
	})
}

func TestHandleUpdateRuntimeConfig(t *testing.T) {
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		PrivateAPIToken:      "old-token",
		PrivateAPIUserAgent:  "old-agent",
		HarukiProxyUserAgent: "old-proxy-agent",
		HarukiProxyVersion:   "v0.0.1",
		HarukiProxySecret:    "old-proxy-secret",
		HarukiProxyUnpackKey: "old-unpack",
		WebhookJWTSecret:     "old-webhook",
	}
	helper.SetPublicAPIAllowedKeys([]string{"old-key"})

	app := fiber.New()
	app.Put("/", handleUpdateRuntimeConfig(helper))

	payload := map[string]any{
		"publicApiAllowedKeys": []string{" key-a ", "key-b"},
		"privateApiToken":      "new-token",
		"privateApiUserAgent":  "new-agent",
		"harukiProxyUserAgent": "new-proxy-agent",
		"harukiProxyVersion":   "v1.2.3",
		"harukiProxySecret":    "new-proxy-secret",
		"harukiProxyUnpackKey": "new-unpack",
		"webhookJwtSecret":     "new-webhook",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	privateAPIToken, privateAPIUserAgent := helper.GetPrivateAPIAuth()
	if privateAPIToken != "new-token" || privateAPIUserAgent != "new-agent" {
		t.Fatalf("private api runtime config not updated: token=%q ua=%q", privateAPIToken, privateAPIUserAgent)
	}
	harukiProxyUserAgent, harukiProxyVersion, harukiProxySecret, harukiProxyUnpackKey := helper.GetHarukiProxyConfig()
	if harukiProxyUserAgent != "new-proxy-agent" || harukiProxyVersion != "v1.2.3" || harukiProxySecret != "new-proxy-secret" || harukiProxyUnpackKey != "new-unpack" {
		t.Fatalf("haruki proxy runtime config not updated")
	}
	if helper.GetWebhookJWTSecret() != "new-webhook" {
		t.Fatalf("webhook secret not updated")
	}
	if len(helper.GetPublicAPIAllowedKeys()) != 2 {
		t.Fatalf("public api keys not updated: %#v", helper.GetPublicAPIAllowedKeys())
	}
}

func newAdminConfigRedisHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *harukiRedis.HarukiRedisManager) {
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
	}
	return helper, redisManager
}

func TestHandleUpdatePublicAPIAllowedKeysClearsPublicCache(t *testing.T) {
	helper, redisManager := newAdminConfigRedisHelper(t)
	if err := redisManager.SetCache(t.Context(), "public_access:/public/jp/suite/1001:query=abc", map[string]any{"x": 1}, time.Minute); err != nil {
		t.Fatalf("seed cache returned error: %v", err)
	}

	app := fiber.New()
	app.Put("/", handleUpdatePublicAPIAllowedKeys(helper))

	body, err := json.Marshal(map[string]any{
		"publicApiAllowedKeys": []string{"key-a"},
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	exists, err := redisManager.Redis.Exists(t.Context(), "public_access:/public/jp/suite/1001:query=abc").Result()
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected public cache entry to be cleared")
	}
}

func TestHandleUpdateRuntimeConfigPropagatesAcrossHelpers(t *testing.T) {
	helper1, redisManager := newAdminConfigRedisHelper(t)
	helper2 := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			Redis: redisManager,
		},
	}

	app := fiber.New()
	app.Put("/", handleUpdateRuntimeConfig(helper1))

	body, err := json.Marshal(map[string]any{
		"publicApiAllowedKeys": []string{"shared-key"},
		"privateApiToken":      "shared-private-token",
		"webhookJwtSecret":     "shared-webhook-secret",
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	privateAPIToken, _ := helper2.GetPrivateAPIAuth()
	if privateAPIToken != "shared-private-token" {
		t.Fatalf("helper2 private api token = %q, want %q", privateAPIToken, "shared-private-token")
	}
	if helper2.GetWebhookJWTSecret() != "shared-webhook-secret" {
		t.Fatalf("helper2 webhook secret = %q, want %q", helper2.GetWebhookJWTSecret(), "shared-webhook-secret")
	}
	keys := helper2.GetPublicAPIAllowedKeys()
	if len(keys) != 1 || keys[0] != "shared-key" {
		t.Fatalf("helper2 public api keys = %#v, want [shared-key]", keys)
	}
}
