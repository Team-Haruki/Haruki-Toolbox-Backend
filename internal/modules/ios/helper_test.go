package ios

import (
	harukiConfig "haruki-suite/config"
	iosGen "haruki-suite/utils/api/ios"
	"testing"
)

func TestGetEndpoint(t *testing.T) {
	originalDirect := harukiConfig.Cfg.Backend.BackendURL
	originalCDN := harukiConfig.Cfg.Backend.BackendCDNURL
	t.Cleanup(func() {
		harukiConfig.Cfg.Backend.BackendURL = originalDirect
		harukiConfig.Cfg.Backend.BackendCDNURL = originalCDN
	})

	harukiConfig.Cfg.Backend.BackendURL = "https://direct.example"
	harukiConfig.Cfg.Backend.BackendCDNURL = "https://cdn.example"

	if got := getEndpoint(iosGen.EndpointTypeDirect); got != "https://direct.example" {
		t.Fatalf("direct endpoint = %q, want %q", got, "https://direct.example")
	}
	if got := getEndpoint(iosGen.EndpointTypeCDN); got != "https://cdn.example" {
		t.Fatalf("cdn endpoint = %q, want %q", got, "https://cdn.example")
	}

	harukiConfig.Cfg.Backend.BackendCDNURL = ""
	if got := getEndpoint(iosGen.EndpointTypeCDN); got != "https://direct.example" {
		t.Fatalf("cdn fallback endpoint = %q, want %q", got, "https://direct.example")
	}
}
