package oauth2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

func TestHandleHydraAuthorizeRedirect(t *testing.T) {
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		PublicURL: "https://hydra.example.com",
	})

	app := fiber.New()
	app.Get("/api/oauth2/authorize", handleHydraAuthorizeRedirect(hydraConfig))

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

	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		PublicURL: server.URL,
	})

	app := fiber.New()
	app.Post("/api/oauth2/token", handleHydraPublicProxy(hydraConfig, "/oauth2/token"))

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

func TestHandleHydraGetConsentRequestSubjectMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/admin/oauth2/auth/requests/consent" {
			clientID, clientSecret, ok := r.BasicAuth()
			if !ok || clientID != "hydra-client" || clientSecret != "hydra-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"challenge":"test","subject":"u2","requested_scope":["user:read"],"requested_access_token_audience":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		AdminURL:       server.URL,
		ClientID:       "hydra-client",
		ClientSecret:   "hydra-secret",
		RequestTimeout: 5 * time.Second,
	})

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "u1")
		return c.Next()
	})
	app.Get("/", handleHydraGetConsentRequest(hydraConfig))

	req := httptest.NewRequest(http.MethodGet, "/?consent_challenge=test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}

func TestHandleHydraRejectConsentSubjectMismatch(t *testing.T) {
	rejectCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/oauth2/auth/requests/consent":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"challenge":"test","subject":"u2","requested_scope":["user:read"],"requested_access_token_audience":[]}`))
			return
		case r.Method == http.MethodPut && r.URL.Path == "/admin/oauth2/auth/requests/consent/reject":
			rejectCalled = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"redirect_to":"https://example.com/callback"}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		AdminURL:       server.URL,
		RequestTimeout: 5 * time.Second,
	})

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "u1")
		return c.Next()
	})
	app.Post("/", handleHydraRejectConsent(hydraConfig))

	req := httptest.NewRequest(http.MethodPost, "/?consent_challenge=test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
	if rejectCalled {
		t.Fatalf("consent reject should not be forwarded for mismatched subject")
	}
}

func TestRegisterHydraRoutesLoginRejectRequiresSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/admin/oauth2/auth/requests/login/reject" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"redirect_to":"https://example.com/callback"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		AdminURL:       server.URL,
		RequestTimeout: 5 * time.Second,
	})

	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		Router:         app,
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, "test-sign-key"),
	}
	registerHydraOAuth2Routes(apiHelper, hydraConfig)

	req := httptest.NewRequest(http.MethodPost, "/api/oauth2/login/reject?login_challenge=test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestHydraConfigRequestTimeoutIsScopedPerInstance(t *testing.T) {
	configA := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{RequestTimeout: 8 * time.Second})
	configB := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{RequestTimeout: 19 * time.Second})

	if got := configA.RequestTimeout(); got != 8*time.Second {
		t.Fatalf("config A timeout = %s, want %s", got, 8*time.Second)
	}
	if got := configB.RequestTimeout(); got != 19*time.Second {
		t.Fatalf("config B timeout = %s, want %s", got, 19*time.Second)
	}
	if got := configA.RequestTimeout(); got != 8*time.Second {
		t.Fatalf("config A timeout changed to %s after constructing config B", got)
	}
}
