package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"haruki-suite/utils/database"
	"haruki-suite/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/mattn/go-sqlite3"

	harukiAPIHelper "haruki-suite/utils/api"
)

func TestValidateWebhookUser(t *testing.T) {
	t.Run("secret not configured", func(t *testing.T) {
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
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
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetWebhookJWTSecret("test-secret")
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
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
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetWebhookJWTSecret("test-secret")
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
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
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetWebhookJWTSecret("test-secret")
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
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
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetWebhookJWTSecret("test-secret")
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
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

	t.Run("runtime secret update takes effect immediately", func(t *testing.T) {
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetWebhookJWTSecret("test-secret")

		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		reqBefore := httptest.NewRequest(http.MethodGet, "/", nil)
		respBefore, err := app.Test(reqBefore)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respBefore.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", respBefore.StatusCode, fiber.StatusUnauthorized)
		}

		helper.SetWebhookJWTSecret("")

		reqAfter := httptest.NewRequest(http.MethodGet, "/", nil)
		respAfter, err := app.Test(reqAfter)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respAfter.StatusCode != fiber.StatusInternalServerError {
			t.Fatalf("status code = %d, want %d", respAfter.StatusCode, fiber.StatusInternalServerError)
		}
	})

	t.Run("nil db client returns internal error after token validation", func(t *testing.T) {
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetWebhookJWTSecret("test-secret")
		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		tokenStr := mustSignHS256Token(t, jwt.MapClaims{
			"_id":        "id",
			"credential": "cred",
		}, "test-secret")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Haruki-Suite-Webhook-Token", tokenStr)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusInternalServerError {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusInternalServerError)
		}
	})

	t.Run("valid webhook token authenticates against postgresql", func(t *testing.T) {
		helper := newWebhookRouteTestHelper(t)
		helper.SetWebhookJWTSecret("test-secret")

		if _, err := helper.DBManager.DB.WebhookEndpoint.Create().
			SetID("507f1f77bcf86cd799439011").
			SetCredential("cred-1").
			SetCallbackURL("https://example.com/webhook").
			Save(context.Background()); err != nil {
			t.Fatalf("create webhook endpoint returned error: %v", err)
		}

		app := fiber.New()
		app.Get("/",
			ValidateWebhookUser(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		tokenStr := mustSignHS256Token(t, jwt.MapClaims{
			"_id":        "507f1f77bcf86cd799439011",
			"credential": "cred-1",
		}, "test-secret")

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Haruki-Suite-Webhook-Token", tokenStr)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})
}

func TestResolveWebhookIDFromLocals(t *testing.T) {
	t.Run("missing local returns unauthorized response", func(t *testing.T) {
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			if _, ok := resolveWebhookIDFromLocals(c); !ok {
				return nil
			}
			return c.SendStatus(fiber.StatusNoContent)
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("valid local passes", func(t *testing.T) {
		app := fiber.New()
		app.Use(func(c fiber.Ctx) error {
			c.Locals("webhook_id", "test-webhook")
			return c.Next()
		})
		app.Get("/", func(c fiber.Ctx) error {
			webhookID, ok := resolveWebhookIDFromLocals(c)
			if !ok {
				return nil
			}
			if webhookID != "test-webhook" {
				return c.SendStatus(fiber.StatusInternalServerError)
			}
			return c.SendStatus(fiber.StatusNoContent)
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
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

func newWebhookRouteTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()

	dsn := "file:webhook-route-test?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			DB: client,
		},
	}
}
