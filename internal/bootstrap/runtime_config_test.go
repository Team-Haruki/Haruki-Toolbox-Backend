package bootstrap

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

func TestRuntimeConfigCompositionCopiesMutableValues(t *testing.T) {
	cfg := harukiConfig.Config{}
	cfg.Others.AllowedKeys = []string{"key-a", "key-b"}
	cfg.MongoDB.PrivateApiSecret = "private-token"
	cfg.MongoDB.PrivateApiUserAgent = "private-agent"
	cfg.HarukiProxy.UserAgent = "proxy-agent"
	cfg.HarukiProxy.Version = "v1.2.3"
	cfg.HarukiProxy.Secret = "proxy-secret"
	cfg.HarukiProxy.UnpackKey = "proxy-unpack-key"
	cfg.Webhook.JWTSecret = "webhook-secret"
	cfg.Webhook.Enabled = true

	service := newRuntimeConfigService(cfg, nil)
	cfg.Others.AllowedKeys[0] = "mutated-after-build"
	cfg.Webhook.Enabled = false

	snapshot, err := service.Current(t.Context())
	if err != nil {
		t.Fatalf("Current returned error: %v", err)
	}
	if !reflect.DeepEqual(snapshot.AllowedKeys, []string{"key-a", "key-b"}) {
		t.Fatalf("AllowedKeys = %#v, want copied startup values", snapshot.AllowedKeys)
	}
	if snapshot.WebhookEnabled == nil || !*snapshot.WebhookEnabled {
		t.Fatalf("WebhookEnabled = %v, want independent true pointer", snapshot.WebhookEnabled)
	}

	snapshot.AllowedKeys[0] = "mutated-return-value"
	*snapshot.WebhookEnabled = false
	again, err := service.Current(t.Context())
	if err != nil {
		t.Fatalf("second Current returned error: %v", err)
	}
	if again.AllowedKeys[0] != "key-a" || again.WebhookEnabled == nil || !*again.WebhookEnabled {
		t.Fatalf("service leaked mutable snapshot values: %#v", again)
	}
}

func TestRuntimeConfigRedisContractRemainsStable(t *testing.T) {
	cfg := harukiConfig.Config{}
	cfg.Webhook.Enabled = true
	payload, err := json.Marshal(runtimeConfigSnapshotFromConfig(cfg))
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	gotFields := make([]string, 0, len(fields))
	for field := range fields {
		gotFields = append(gotFields, field)
	}
	sort.Strings(gotFields)
	wantFields := []string{
		"harukiProxySecret",
		"harukiProxyUnpackKey",
		"harukiProxyUserAgent",
		"harukiProxyVersion",
		"privateApiToken",
		"privateApiUserAgent",
		"publicApiAllowedKeys",
		"webhookEnabled",
		"webhookJwtSecret",
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("runtime config JSON fields = %#v, want %#v", gotFields, wantFields)
	}
	if got := harukiRedis.BuildRuntimeConfigKey(); got != "haruki:config:runtime" {
		t.Fatalf("runtime config Redis key = %q, want %q", got, "haruki:config:runtime")
	}
}
