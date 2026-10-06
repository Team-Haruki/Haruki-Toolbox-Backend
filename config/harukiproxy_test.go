package config

import (
	"gopkg.in/yaml.v3"
	"testing"
)

func TestProxyPolicyYAML(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("haruki_proxy: {}"), &c); err != nil || c.HarukiProxy.V3ClientPolicy != nil {
		t.Fatal("omitted policy not preserved")
	}
	if err := yaml.Unmarshal([]byte("haruki_proxy:\n  v3_client_policy:\n    allowed_channels: []\n    minimum_versions: {}\n"), &c); err != nil || c.HarukiProxy.V3ClientPolicy == nil {
		t.Fatal("explicit empty policy not preserved")
	}
	for _, key := range []string{"require_platform", "allow_channels", "unknown"} {
		if err := yaml.Unmarshal([]byte("haruki_proxy:\n  v3_client_policy:\n    "+key+": false\n"), &c); err == nil {
			t.Fatalf("unknown policy key accepted: %s", key)
		}
	}
}
