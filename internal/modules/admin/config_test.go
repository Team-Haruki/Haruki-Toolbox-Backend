package admin

import (
	"bytes"
	"encoding/json"
	harukiConfig "haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
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
	originalCfg := append([]string(nil), harukiConfig.Cfg.Others.PublicAPIAllowedKeys...)
	defer func() {
		harukiConfig.Cfg.Others.PublicAPIAllowedKeys = originalCfg
	}()

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
	if len(harukiConfig.Cfg.Others.PublicAPIAllowedKeys) != 2 || harukiConfig.Cfg.Others.PublicAPIAllowedKeys[0] != "key-a" || harukiConfig.Cfg.Others.PublicAPIAllowedKeys[1] != "key-b" {
		t.Fatalf("config keys not updated as expected: %#v", harukiConfig.Cfg.Others.PublicAPIAllowedKeys)
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
	originalPublicKeys := append([]string(nil), harukiConfig.Cfg.Others.PublicAPIAllowedKeys...)
	originalPrivateToken := harukiConfig.Cfg.MongoDB.PrivateApiSecret
	originalPrivateUA := harukiConfig.Cfg.MongoDB.PrivateApiUserAgent
	originalProxyUA := harukiConfig.Cfg.HarukiProxy.UserAgent
	originalProxyVersion := harukiConfig.Cfg.HarukiProxy.Version
	originalProxySecret := harukiConfig.Cfg.HarukiProxy.Secret
	originalProxyUnpackKey := harukiConfig.Cfg.HarukiProxy.UnpackKey
	originalWebhookSecret := harukiConfig.Cfg.Webhook.JWTSecret
	defer func() {
		harukiConfig.Cfg.Others.PublicAPIAllowedKeys = originalPublicKeys
		harukiConfig.Cfg.MongoDB.PrivateApiSecret = originalPrivateToken
		harukiConfig.Cfg.MongoDB.PrivateApiUserAgent = originalPrivateUA
		harukiConfig.Cfg.HarukiProxy.UserAgent = originalProxyUA
		harukiConfig.Cfg.HarukiProxy.Version = originalProxyVersion
		harukiConfig.Cfg.HarukiProxy.Secret = originalProxySecret
		harukiConfig.Cfg.HarukiProxy.UnpackKey = originalProxyUnpackKey
		harukiConfig.Cfg.Webhook.JWTSecret = originalWebhookSecret
	}()

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
