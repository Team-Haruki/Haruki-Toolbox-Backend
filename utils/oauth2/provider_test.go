package oauth2

import (
	"testing"
	"time"
)

func TestHydraConfigDefaultsProvider(t *testing.T) {
	config := NewHydraConfig(HydraConfigOptions{})
	if got := config.Provider(); got != ProviderHydra {
		t.Fatalf("Provider() = %q, want %q", got, ProviderHydra)
	}
	if !config.Enabled() {
		t.Fatal("default Hydra config should be enabled")
	}

	config = NewHydraConfig(HydraConfigOptions{Provider: "HyDrA"})
	if got := config.Provider(); got != ProviderHydra {
		t.Fatalf("Provider() = %q, want %q", got, ProviderHydra)
	}

	config = NewHydraConfig(HydraConfigOptions{Provider: "builtin"})
	if config.Enabled() {
		t.Fatal("non-Hydra provider should be disabled")
	}
}

func TestBuildHydraEndpoint(t *testing.T) {
	t.Run("join root base URL", func(t *testing.T) {
		got, err := buildHydraEndpoint("http://hydra:4444", "/oauth2/token")
		if err != nil {
			t.Fatalf("buildHydraEndpoint returned error: %v", err)
		}
		if got != "http://hydra:4444/oauth2/token" {
			t.Fatalf("buildHydraEndpoint = %q", got)
		}
	})

	t.Run("preserve prefixed base path", func(t *testing.T) {
		got, err := buildHydraEndpoint("https://auth.example.com/hydra", "/oauth2/revoke")
		if err != nil {
			t.Fatalf("buildHydraEndpoint returned error: %v", err)
		}
		if got != "https://auth.example.com/hydra/oauth2/revoke" {
			t.Fatalf("buildHydraEndpoint = %q", got)
		}
	})

	t.Run("reject invalid base URL", func(t *testing.T) {
		if _, err := buildHydraEndpoint("/relative", "/oauth2/token"); err == nil {
			t.Fatal("buildHydraEndpoint should fail for invalid base URL")
		}
	})
}

func TestHydraConfigBrowserEndpointFallback(t *testing.T) {
	config := NewHydraConfig(HydraConfigOptions{
		PublicURL:  "http://hydra:4444",
		BrowserURL: "https://gateway.example.com",
	})
	got, err := config.BrowserEndpoint("/oauth2/auth")
	if err != nil {
		t.Fatalf("BrowserEndpoint returned error: %v", err)
	}
	if got != "https://gateway.example.com/oauth2/auth" {
		t.Fatalf("BrowserEndpoint() = %q", got)
	}

	config = NewHydraConfig(HydraConfigOptions{PublicURL: "http://hydra:4444"})
	got, err = config.BrowserEndpoint("/oauth2/auth")
	if err != nil {
		t.Fatalf("BrowserEndpoint fallback returned error: %v", err)
	}
	if got != "http://hydra:4444/oauth2/auth" {
		t.Fatalf("BrowserEndpoint() fallback = %q", got)
	}
}

func TestHydraConfigCopiesCredentialsAndTimeout(t *testing.T) {
	config := NewHydraConfig(HydraConfigOptions{
		ClientID:     " client-id ",
		ClientSecret: "client-secret",
	})
	clientID, clientSecret := config.ClientCredentials()
	if clientID != "client-id" || clientSecret != "client-secret" {
		t.Fatalf("ClientCredentials() = (%q, %q)", clientID, clientSecret)
	}
	if got := config.RequestTimeout(); got != 10*time.Second {
		t.Fatalf("RequestTimeout() = %s, want %s", got, 10*time.Second)
	}

	config = NewHydraConfig(HydraConfigOptions{RequestTimeout: 27 * time.Second})
	if got := config.RequestTimeout(); got != 27*time.Second {
		t.Fatalf("RequestTimeout() = %s, want %s", got, 27*time.Second)
	}
}
