package oauth2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

// logoutTestServer stands in for Hydra's admin API and records what it was asked.
func logoutTestServer(t *testing.T, status int, body string) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotPath, gotChallenge string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotChallenge = r.URL.Query().Get("logout_challenge")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &gotPath, &gotChallenge
}

func TestHandleHydraGetLogoutRequestForwardsChallenge(t *testing.T) {
	srv, gotPath, gotChallenge := logoutTestServer(t, http.StatusOK,
		`{"challenge":"c1","subject":"user-1","sid":"s1","rp_initiated":true,"client":{"client_id":"example-site"}}`)
	cfg := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: srv.URL})

	app := fiber.New()
	app.Get("/api/oauth2/logout", handleHydraGetLogoutRequest(cfg))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/oauth2/logout?logout_challenge=c1", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if *gotPath != "/admin/oauth2/auth/requests/logout" {
		t.Fatalf("hydra path = %q", *gotPath)
	}
	if *gotChallenge != "c1" {
		t.Fatalf("challenge forwarded = %q, want c1", *gotChallenge)
	}
	body, _ := io.ReadAll(resp.Body)
	// The frontend keys its wording off client presence, so it has to survive.
	if !strings.Contains(string(body), "example-site") {
		t.Fatalf("client details missing from response: %s", body)
	}
}

func TestHandleHydraAcceptLogoutReturnsRedirect(t *testing.T) {
	srv, gotPath, _ := logoutTestServer(t, http.StatusOK, `{"redirect_to":"https://rp.example.com/after-logout"}`)
	cfg := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: srv.URL})

	app := fiber.New()
	app.Post("/api/oauth2/logout/accept", handleHydraAcceptLogout(cfg))

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/api/oauth2/logout/accept?logout_challenge=c2", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if *gotPath != "/admin/oauth2/auth/requests/logout/accept" {
		t.Fatalf("hydra path = %q", *gotPath)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "https://rp.example.com/after-logout") {
		t.Fatalf("redirect_to missing: %s", body)
	}
}

// Hydra answers reject with 204 and no body. Decoding that as a redirect the way
// accept does would fail, so reject must not go through sendHydraAdminJSON.
func TestHandleHydraRejectLogoutToleratesEmptyBody(t *testing.T) {
	srv, gotPath, gotChallenge := logoutTestServer(t, http.StatusNoContent, "")
	cfg := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: srv.URL})

	app := fiber.New()
	app.Post("/api/oauth2/logout/reject", handleHydraRejectLogout(cfg))

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/api/oauth2/logout/reject?logout_challenge=c3", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 — an empty 204 from Hydra must not surface as an error", resp.StatusCode)
	}
	if *gotPath != "/admin/oauth2/auth/requests/logout/reject" {
		t.Fatalf("hydra path = %q", *gotPath)
	}
	if *gotChallenge != "c3" {
		t.Fatalf("challenge forwarded = %q, want c3", *gotChallenge)
	}
}

func TestLogoutHandlersRequireChallenge(t *testing.T) {
	cfg := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: "https://hydra.invalid"})
	app := fiber.New()
	app.Get("/api/oauth2/logout", handleHydraGetLogoutRequest(cfg))
	app.Post("/api/oauth2/logout/accept", handleHydraAcceptLogout(cfg))
	app.Post("/api/oauth2/logout/reject", handleHydraRejectLogout(cfg))

	for _, c := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/oauth2/logout"},
		{http.MethodPost, "/api/oauth2/logout/accept"},
		{http.MethodPost, "/api/oauth2/logout/reject"},
	} {
		resp, err := app.Test(httptest.NewRequest(c.method, c.path, nil))
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("%s %s status = %d, want 400 without a challenge", c.method, c.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
