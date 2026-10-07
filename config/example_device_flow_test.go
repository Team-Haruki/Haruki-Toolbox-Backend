package config

import (
	"reflect"
	"testing"
)

// The example file documents every oauth2.device_flow key with its default.
func TestExampleConfigDeviceFlowMatchesDefaults(t *testing.T) {
	cfg, err := Load("../haruki-toolbox-configs.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	got := cfg.OAuth2.DeviceFlow
	want := defaultOAuth2DeviceFlowConfig()
	// The example spells the empty lists out; the defaults leave them nil.
	if len(got.ClientAllowlist) != 0 || len(got.AllowedOrigins) != 0 {
		t.Fatalf("example lists are not empty: %+v", got)
	}
	got.ClientAllowlist, got.AllowedOrigins = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("example oauth2.device_flow = %+v\nwant defaults %+v", got, want)
	}
}
