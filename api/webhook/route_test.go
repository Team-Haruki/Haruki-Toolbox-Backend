package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

func TestValidateWebhookUser(t *testing.T) {
	t.Run("secret not configured", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser("", nil),
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

	t.Run("missing webhook token header", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser("test-secret", nil),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("invalid jwt token", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser("test-secret", nil),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Haruki-Suite-Webhook-Token", "not-a-jwt")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("token payload missing required fields", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser("test-secret", nil),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		tokenStr := mustSignHS256Token(t, jwt.MapClaims{
			"foo": "bar",
		}, "test-secret")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Haruki-Suite-Webhook-Token", tokenStr)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("unsupported signing method rejected", func(t *testing.T) {
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser("test-secret", nil),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		token := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
			"_id":        "id",
			"credential": "cred",
		})
		tokenStr, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("SignedString returned error: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Haruki-Suite-Webhook-Token", tokenStr)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})
}

func mustSignHS256Token(t *testing.T, claims jwt.MapClaims, secret string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}
	return tokenStr
}
