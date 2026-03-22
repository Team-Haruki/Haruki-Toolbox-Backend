package api

import (
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"testing"

	goredis "github.com/redis/go-redis/v9"
)

func TestPublicAPIAllowedKeysCopySemantics(t *testing.T) {
	helper := &HarukiToolboxRouterHelpers{}

	input := []string{"a", "b"}
	helper.SetPublicAPIAllowedKeys(input)
	input[0] = "mutated"

	stored := helper.GetPublicAPIAllowedKeys()
	if len(stored) != 2 || stored[0] != "a" || stored[1] != "b" {
		t.Fatalf("stored keys mismatch: %#v", stored)
	}

	stored[1] = "changed"
	again := helper.GetPublicAPIAllowedKeys()
	if len(again) != 2 || again[0] != "a" || again[1] != "b" {
		t.Fatalf("GetPublicAPIAllowedKeys leaked internal slice: %#v", again)
	}
}

func TestNewHarukiToolboxRouterHelpersCopiesPublicKeys(t *testing.T) {
	input := []string{"a", "b"}
	helper := NewHarukiToolboxRouterHelpers(
		nil,
		nil,
		nil,
		nil,
		nil,
		input,
		"private-token",
		"private-agent",
		"proxy-agent",
		"v1",
		"proxy-secret",
		"proxy-unpack-key",
		"webhook-secret",
		true,
	)

	input[0] = "mutated"
	keys := helper.GetPublicAPIAllowedKeys()
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("constructor did not copy public keys: %#v", keys)
	}
}

func TestRuntimeConfigGettersAndSetters(t *testing.T) {
	helper := &HarukiToolboxRouterHelpers{}

	helper.SetPrivateAPIToken("private-token")
	helper.SetPrivateAPIUserAgent("private-agent")
	token, userAgent := helper.GetPrivateAPIAuth()
	if token != "private-token" || userAgent != "private-agent" {
		t.Fatalf("private api auth mismatch: token=%q userAgent=%q", token, userAgent)
	}

	helper.SetHarukiProxyUserAgent("proxy-agent")
	helper.SetHarukiProxyVersion("v1.2.3")
	helper.SetHarukiProxySecret("proxy-secret")
	helper.SetHarukiProxyUnpackKey("proxy-unpack")
	harukiProxyUserAgent, harukiProxyVersion, harukiProxySecret, harukiProxyUnpackKey := helper.GetHarukiProxyConfig()
	if harukiProxyUserAgent != "proxy-agent" || harukiProxyVersion != "v1.2.3" || harukiProxySecret != "proxy-secret" || harukiProxyUnpackKey != "proxy-unpack" {
		t.Fatalf("haruki proxy config mismatch")
	}

	helper.SetWebhookJWTSecret("webhook-secret")
	if helper.GetWebhookJWTSecret() != "webhook-secret" {
		t.Fatalf("webhook secret mismatch")
	}
	if !helper.GetWebhookEnabled() {
		t.Fatalf("webhook enabled should default to true")
	}
	helper.SetWebhookEnabled(false)
	if helper.GetWebhookEnabled() {
		t.Fatalf("webhook enabled flag not updated")
	}
}

func TestRedisClientNilSafe(t *testing.T) {
	var nilHelper *HarukiToolboxRouterHelpers
	if got := nilHelper.RedisClient(); got != nil {
		t.Fatalf("nil helper RedisClient() = %v, want nil", got)
	}

	helper := &HarukiToolboxRouterHelpers{}
	if got := helper.RedisClient(); got != nil {
		t.Fatalf("helper without db manager RedisClient() = %v, want nil", got)
	}

	helper.DBManager = &database.HarukiToolboxDBManager{}
	if got := helper.RedisClient(); got != nil {
		t.Fatalf("helper without redis manager RedisClient() = %v, want nil", got)
	}

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:6379"})
	defer func() { _ = client.Close() }()
	helper.DBManager.Redis = &harukiRedis.HarukiRedisManager{Redis: client}
	if got := helper.RedisClient(); got != client {
		t.Fatalf("RedisClient() = %v, want %v", got, client)
	}
}
