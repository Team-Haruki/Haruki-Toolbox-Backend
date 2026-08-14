package oauth2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestVerifyOAuth2TokenViaHydraIntrospection(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"active":true,"sub":"u-1","client_id":"c-1","scope":"user:read bindings:read","token_use":"access_token"}`))
		case "token-missing-type":
			_, _ = w.Write([]byte(`{"active":true,"sub":"u-1","client_id":"c-1","scope":"user:read"}`))
		case "token-refresh":
			_, _ = w.Write([]byte(`{"active":true,"sub":"u-1","client_id":"c-1","scope":"user:read","token_use":"refresh_token"}`))
		case "token-missing-client":
			_, _ = w.Write([]byte(`{"active":true,"sub":"u-1","scope":"user:read","token_use":"access_token"}`))
		default:
			_, _ = w.Write([]byte(`{"active":false}`))
		}
	}))
	t.Cleanup(server.Close)

	hydraConfig := NewHydraConfig(HydraConfigOptions{AdminURL: server.URL, RequestTimeout: 5 * time.Second})

	app := fiber.New()
	app.Get("/ok", VerifyOAuth2Token(hydraConfig, nil, ScopeUserRead, nil), func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"userID":   c.Locals("userID"),
			"clientID": c.Locals("oauth2ClientID"),
		})
	})
	app.Get("/scope", VerifyOAuth2Token(hydraConfig, nil, ScopeGameDataRead, nil), func(c fiber.Ctx) error {
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

	for _, token := range []string{"token-missing-type", "token-refresh", "token-missing-client"} {
		req = httptest.NewRequest(http.MethodGet, "/ok", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = app.Test(req)
		if err != nil {
			t.Fatalf("app.Test(%s) returned error: %v", token, err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status for %s = %d, want %d", token, resp.StatusCode, fiber.StatusUnauthorized)
		}
	}
}

func TestVerifyOAuth2TokenSetsBearerChallengeOnMissingAuthorization(t *testing.T) {
	app := fiber.New()
	app.Get("/protected", VerifyOAuth2Token(NewHydraConfig(HydraConfigOptions{}), nil, ScopeUserRead, nil), func(c fiber.Ctx) error {
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

func TestVerifyOAuth2TokenScopesClientCheckerToMiddleware(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/introspect" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"sub":"u-1","client_id":"disabled-client","scope":"user:read","token_use":"access_token"}`))
	}))
	t.Cleanup(server.Close)

	hydraConfig := NewHydraConfig(HydraConfigOptions{AdminURL: server.URL, RequestTimeout: 5 * time.Second})

	checkedClientID := ""
	checker := func(_ context.Context, clientID string) (bool, error) {
		checkedClientID = clientID
		return false, nil
	}

	app := fiber.New()
	app.Get("/checked", VerifyOAuth2Token(hydraConfig, nil, ScopeUserRead, checker), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})
	app.Get("/unchecked", VerifyOAuth2Token(hydraConfig, nil, ScopeUserRead, nil), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	checkedReq := httptest.NewRequest(http.MethodGet, "/checked", nil)
	checkedReq.Header.Set("Authorization", "Bearer token-active")
	checkedResp, err := app.Test(checkedReq)
	if err != nil {
		t.Fatalf("checked app.Test returned error: %v", err)
	}
	if checkedResp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("checked status = %d, want %d", checkedResp.StatusCode, fiber.StatusUnauthorized)
	}
	if checkedClientID != "disabled-client" {
		t.Fatalf("checked client id = %q, want disabled-client", checkedClientID)
	}

	uncheckedReq := httptest.NewRequest(http.MethodGet, "/unchecked", nil)
	uncheckedReq.Header.Set("Authorization", "Bearer token-active")
	uncheckedResp, err := app.Test(uncheckedReq)
	if err != nil {
		t.Fatalf("unchecked app.Test returned error: %v", err)
	}
	if uncheckedResp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("unchecked status = %d, want %d", uncheckedResp.StatusCode, fiber.StatusNoContent)
	}
}
