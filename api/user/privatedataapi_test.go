package user

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestValidateUserPermission(t *testing.T) {
	t.Run("missing token config fails closed", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission("", ""),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusInternalServerError {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusInternalServerError)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission("expected-token", ""),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "wrong-token")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("invalid user agent", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission("expected-token", "HarukiProxy"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "expected-token")
		req.Header.Set("User-Agent", "curl/8.0")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("authorized request passes", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission("expected-token", "HarukiProxy"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "expected-token")
		req.Header.Set("User-Agent", "HarukiProxy/v1.0.0")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})
}

func TestProcessRequestKeys(t *testing.T) {
	base := map[string]any{
		"a": float64(1),
		"b": "two",
	}

	makeApp := func() *fiber.App {
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			return processRequestKeys(c, base)
		})
		return app
	}

	t.Run("without key returns full payload", func(t *testing.T) {
		app := makeApp()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}

		var got map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode response failed: %v", err)
		}
		if got["a"] != float64(1) || got["b"] != "two" {
			t.Fatalf("unexpected response body: %#v", got)
		}
	})

	t.Run("single key returns direct value", func(t *testing.T) {
		app := makeApp()
		req := httptest.NewRequest(http.MethodGet, "/?key=a", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}

		var got any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode response failed: %v", err)
		}
		if got != float64(1) {
			t.Fatalf("unexpected single-key value: %#v", got)
		}
	})

	t.Run("multi key returns selected map", func(t *testing.T) {
		app := makeApp()
		req := httptest.NewRequest(http.MethodGet, "/?key=a,c", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}

		var got map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode response failed: %v", err)
		}
		if got["a"] != float64(1) {
			t.Fatalf("expected key a to equal 1, got %#v", got["a"])
		}
		if _, exists := got["c"]; !exists {
			t.Fatalf("expected missing key c to be present with null value")
		}
		if got["c"] != nil {
			t.Fatalf("expected key c to be null, got %#v", got["c"])
		}
	})
}
