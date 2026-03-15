package oauth2

import (
	"haruki-suite/config"
	"testing"
	"time"
)

func TestOAuth2Provider(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	config.Cfg.OAuth2.Provider = ""
	if got := OAuth2Provider(); got != ProviderHydra {
		t.Fatalf("OAuth2Provider() = %q, want %q", got, ProviderHydra)
	}

	config.Cfg.OAuth2.Provider = "HyDrA"
	if got := OAuth2Provider(); got != ProviderHydra {
		t.Fatalf("OAuth2Provider() = %q, want %q", got, ProviderHydra)
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
			t.Fatalf("buildHydraEndpoint should fail for invalid base URL")
		}
	})
}

func TestHydraBrowserEndpoint(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	config.Cfg.OAuth2.HydraPublicURL = "http://hydra:4444"
	config.Cfg.OAuth2.HydraBrowserURL = "https://gateway.example.com"

	got, err := HydraBrowserEndpoint("/oauth2/auth")
	if err != nil {
		t.Fatalf("HydraBrowserEndpoint returned error: %v", err)
	}
	if got != "https://gateway.example.com/oauth2/auth" {
		t.Fatalf("HydraBrowserEndpoint() = %q", got)
	}

	config.Cfg.OAuth2.HydraBrowserURL = ""
	got, err = HydraBrowserEndpoint("/oauth2/auth")
	if err != nil {
		t.Fatalf("HydraBrowserEndpoint fallback returned error: %v", err)
	}
	if got != "http://hydra:4444/oauth2/auth" {
		t.Fatalf("HydraBrowserEndpoint() fallback = %q", got)
	}
}

func TestHydraRequestTimeout(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	config.Cfg.OAuth2.HydraRequestTimeoutSecond = 0
	if got := HydraRequestTimeout(); got != 10*time.Second {
		t.Fatalf("HydraRequestTimeout() = %s, want %s", got, 10*time.Second)
	}

	config.Cfg.OAuth2.HydraRequestTimeoutSecond = 27
	if got := HydraRequestTimeout(); got != 27*time.Second {
		t.Fatalf("HydraRequestTimeout() = %s, want %s", got, 27*time.Second)
	}
}
