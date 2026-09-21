package ios

import (
	"testing"

	iosGen "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/ios"
)

func TestEndpointConfigSelectsDirectAndCDNEndpoints(t *testing.T) {
	config := NewEndpointConfig(EndpointConfigOptions{
		BackendURL:    "https://direct.example",
		BackendCDNURL: "https://cdn.example",
	})

	if got := config.endpoint(iosGen.EndpointTypeDirect); got != "https://direct.example" {
		t.Fatalf("direct endpoint = %q, want %q", got, "https://direct.example")
	}
	if got := config.endpoint(iosGen.EndpointTypeCDN); got != "https://cdn.example" {
		t.Fatalf("cdn endpoint = %q, want %q", got, "https://cdn.example")
	}
}

func TestEndpointConfigFallsBackToBackendURLForEmptyCDN(t *testing.T) {
	config := NewEndpointConfig(EndpointConfigOptions{
		BackendURL: "https://direct.example",
	})

	if got := config.endpoint(iosGen.EndpointTypeCDN); got != "https://direct.example" {
		t.Fatalf("cdn fallback endpoint = %q, want %q", got, "https://direct.example")
	}
}

func TestEndpointConfigPreservesURLStringsVerbatim(t *testing.T) {
	config := NewEndpointConfig(EndpointConfigOptions{
		BackendURL:    " https://direct.example/ ",
		BackendCDNURL: "https://cdn.example/",
	})

	if got := config.endpoint(iosGen.EndpointTypeDirect); got != " https://direct.example/ " {
		t.Fatalf("direct endpoint = %q, want verbatim configured value", got)
	}
	if got := config.endpoint(iosGen.EndpointTypeCDN); got != "https://cdn.example/" {
		t.Fatalf("cdn endpoint = %q, want verbatim configured value", got)
	}
}
