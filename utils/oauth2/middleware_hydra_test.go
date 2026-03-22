package oauth2

import (
	"encoding/json"
	"haruki-suite/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestVerifyOAuth2TokenViaHydraIntrospection(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/introspect" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		token := r.PostForm.Get("token")
		w.Header().Set("Content-Type", "application/json")
		switch token {
		case "token-active":
			_, _ = w.Write([]byte(`{"active":true,"sub":"u-1","client_id":"c-1","scope":"user:read bindings:read"}`))
		default:
			_, _ = w.Write([]byte(`{"active":false}`))
		}
	}))
	t.Cleanup(server.Close)

	config.Cfg.OAuth2.HydraAdminURL = server.URL
	config.Cfg.OAuth2.HydraRequestTimeoutSecond = 5

	app := fiber.New()
	app.Get("/ok", VerifyOAuth2Token(nil, ScopeUserRead), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"userID":   c.Locals("userID"),
			"clientID": c.Locals("oauth2ClientID"),
		})
	})
	app.Get("/scope", VerifyOAuth2Token(nil, ScopeGameDataRead), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer token-active")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	body, _ := io.ReadAll(resp.Body)
	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if decoded["userID"] != "u-1" {
		t.Fatalf("userID = %q, want %q", decoded["userID"], "u-1")
	}
	if decoded["clientID"] != "c-1" {
		t.Fatalf("clientID = %q, want %q", decoded["clientID"], "c-1")
	}

	req = httptest.NewRequest(http.MethodGet, "/scope", nil)
	req.Header.Set("Authorization", "Bearer token-active")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}

	req = httptest.NewRequest(http.MethodGet, "/ok", nil)
	req.Header.Set("Authorization", "Bearer token-inactive")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestVerifySessionOrOAuth2TokenFallsBackToSessionWithoutBearer(t *testing.T) {
	app := fiber.New()
	app.Get("/mixed", VerifySessionOrOAuth2Token(func(c fiber.Ctx) error {
		c.Locals("userID", "session-user")
		return c.Next()
	}, nil, ScopeUserRead), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"userID": c.Locals("userID")})
	})

	req := httptest.NewRequest(http.MethodGet, "/mixed", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	body, _ := io.ReadAll(resp.Body)
	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if decoded["userID"] != "session-user" {
		t.Fatalf("userID = %q, want %q", decoded["userID"], "session-user")
	}
}

func TestVerifyOAuth2TokenSetsBearerChallengeOnMissingAuthorization(t *testing.T) {
	app := fiber.New()
	app.Get("/protected", VerifyOAuth2Token(nil, ScopeUserRead), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer realm=") {
		t.Fatalf("WWW-Authenticate = %q, want bearer challenge", got)
	}
}
