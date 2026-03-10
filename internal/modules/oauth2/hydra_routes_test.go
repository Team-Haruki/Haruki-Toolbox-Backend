package oauth2

import (
	"haruki-suite/config"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestHandleHydraAuthorizeRedirect(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})
	config.Cfg.OAuth2.HydraPublicURL = "https://hydra.example.com"

	app := fiber.New()
	app.Get("/api/oauth2/authorize", handleHydraAuthorizeRedirect())

	req := httptest.NewRequest(http.MethodGet, "/api/oauth2/authorize?client_id=test-client&state=abc", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusSeeOther)
	}
	if location := resp.Header.Get("Location"); location != "https://hydra.example.com/oauth2/auth?client_id=test-client&state=abc" {
		t.Fatalf("Location = %q", location)
	}
}

func TestHandleHydraPublicProxy(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	var gotPath string
	var gotAuth string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"abc"}`))
	}))
	t.Cleanup(server.Close)

	config.Cfg.OAuth2.HydraPublicURL = server.URL

	app := fiber.New()
	app.Post("/api/oauth2/token", handleHydraPublicProxy("/oauth2/token"))

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	req := httptest.NewRequest(http.MethodPost, "/api/oauth2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Basic test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}

	if gotPath != "/oauth2/token" {
		t.Fatalf("forwarded path = %q, want %q", gotPath, "/oauth2/token")
	}
	if gotAuth != "Basic test" {
		t.Fatalf("forwarded auth = %q", gotAuth)
	}
	if gotBody != form.Encode() {
		t.Fatalf("forwarded body = %q, want %q", gotBody, form.Encode())
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"access_token":"abc"}` {
		t.Fatalf("response body = %q", string(body))
	}
}

func TestNormalizeGrantedValues(t *testing.T) {
	allowed := []string{"user:read", "bindings:read"}
	requested := []string{"bindings:read", "bindings:read"}
	values, err := normalizeGrantedValues(allowed, requested)
	if err != nil {
		t.Fatalf("normalizeGrantedValues returned error: %v", err)
	}
	if len(values) != 1 || values[0] != "bindings:read" {
		t.Fatalf("normalizeGrantedValues = %#v", values)
	}

	if _, err := normalizeGrantedValues(allowed, []string{"unknown"}); err == nil {
		t.Fatalf("normalizeGrantedValues should fail for unknown grant")
	}
}
