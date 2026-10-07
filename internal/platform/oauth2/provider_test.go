package oauth2

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestHydraConfigDoWithoutRedirect(t *testing.T) {
	var followed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			followed.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "v", Secure: true})
		w.Header().Set("Location", "/next")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)

	cfg := NewHydraConfig(HydraConfigOptions{PublicURL: server.URL})
	req, err := http.NewRequest(http.MethodGet, server.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cfg.DoWithoutRedirect(req)
	if err != nil {
		t.Fatalf("DoWithoutRedirect returned error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/next" || followed.Load() {
		t.Fatalf("status = %d, location = %q, followed = %v", resp.StatusCode, resp.Header.Get("Location"), followed.Load())
	}
	if len(resp.Cookies()) != 1 {
		t.Fatalf("Set-Cookie not returned to the caller: %v", resp.Header.Values("Set-Cookie"))
	}

	var nilConfig *HydraConfig
	if _, err := nilConfig.DoWithoutRedirect(req); err == nil {
		t.Fatal("nil config must refuse")
	}
}
