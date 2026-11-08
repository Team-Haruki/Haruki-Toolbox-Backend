package upload

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"

	"github.com/gofiber/fiber/v3"
)

func newHarukiProxyRoutesApp() *fiber.App {
	app := fiber.New()
	registerHarukiProxyRoutes(&harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app}, Dependencies{})
	return app
}

func doHarukiProxyRoute(t *testing.T, app *fiber.App, path string, headers map[string]string) (*http.Response, proxyTestResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte("sealed-or-raw-payload")))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body proxyTestResponse
	if err := json.UnmarshalRead(resp.Body, &body); err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("X-Request-ID") == "" || body.Data.RequestID != resp.Header.Get("X-Request-ID") {
		t.Fatalf("request id header %q does not match body %q", resp.Header.Get("X-Request-ID"), body.Data.RequestID)
	}
	return resp, body
}

// ★ The shared-key upload paths are closed for good: whatever a request
// carries (a valid-looking secret, User-Agent and body), it is refused with
// protocol_retired and told to log in with OAuth2, before anything reads the
// secret or the body.
func TestHarukiProxySharedKeyPathsAreRetired(t *testing.T) {
	app := newHarukiProxyRoutesApp()
	for _, path := range []string{"/harukiproxy/jp/123/suite/upload", "/api/harukiproxy/tw/456/mysekai/upload", "/harukiproxy/xx/abc/whatever/upload"} {
		t.Run(path, func(t *testing.T) {
			resp, body := doHarukiProxyRoute(t, app, path, map[string]string{"X-Haruki-Toolbox-Secret": "any-shared-key", "User-Agent": "HarukiProxy/v2.2.1"})
			if resp.StatusCode != fiber.StatusGone || body.Data.ErrorCode != proxyRetiredCode || body.Data.Retryable {
				t.Fatalf("got %d %+v, want 410 protocol_retired", resp.StatusCode, body)
			}
			if !strings.Contains(body.Message, "OAuth2") {
				t.Errorf("message %q does not tell the client to log in with OAuth2", body.Message)
			}
			if resp.Header.Get("Sunset") != "Sat, 31 Oct 2026 16:00:00 GMT" {
				t.Errorf("Sunset = %q", resp.Header.Get("Sunset"))
			}
		})
	}
}

// ★ The v3 upload accepts only an OAuth2 bearer token. A request without one
// (a shared-key client, or a custom endpoint set to Haruki Toolbox) gets
// oauth2_required, not an expired-token error; a bearer token goes on to the
// OAuth2 check, never to a shared-key fallback.
func TestHarukiProxyV3RequiresAnOAuth2Login(t *testing.T) {
	app := newHarukiProxyRoutesApp()
	const path = "/harukiproxy/v3/jp/123/suite/upload"
	for name, headers := range map[string]map[string]string{
		"shared key":       {"X-Haruki-Toolbox-Secret": "any-shared-key"},
		"no credentials":   {},
		"basic auth":       {"Authorization": "Basic dXNlcjpwYXNz"},
		"empty auth":       {"Authorization": " "},
		"shared key, /api": {"X-Haruki-Toolbox-Secret": "any-shared-key", "X-Path": "/api"},
	} {
		t.Run(name, func(t *testing.T) {
			p := path
			if headers["X-Path"] == "/api" {
				p = "/api" + path
			}
			resp, body := doHarukiProxyRoute(t, app, p, headers)
			if resp.StatusCode != fiber.StatusUnauthorized || body.Data.ErrorCode != proxyOAuth2RequiredCode || body.Data.Retryable {
				t.Fatalf("got %d %+v, want 401 oauth2_required", resp.StatusCode, body)
			}
			if body.Message != proxyOAuth2RequiredMessage || !strings.HasPrefix(resp.Header.Get(fiber.HeaderWWWAuthenticate), "Bearer") {
				t.Errorf("message %q, WWW-Authenticate %q", body.Message, resp.Header.Get(fiber.HeaderWWWAuthenticate))
			}
		})
	}
	// With a bearer token the request reaches the OAuth2 check (unavailable
	// here, so 503), even when it also carries the shared key.
	for _, scheme := range []string{"Bearer", "bearer"} {
		resp, body := doHarukiProxyRoute(t, app, path, map[string]string{"Authorization": scheme + " token", "X-Haruki-Toolbox-Secret": "any-shared-key"})
		if resp.StatusCode != fiber.StatusServiceUnavailable || body.Data.ErrorCode != "temporarily_unavailable" {
			t.Fatalf("%s: got %d %+v, want the OAuth2 check", scheme, resp.StatusCode, body)
		}
	}
}
