package userprivateapi

import (
	"encoding/json"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestValidateUserPermission(t *testing.T) {
	t.Run("missing token config fails closed", func(t *testing.T) {
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission(helper),
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
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetPrivateAPIToken("expected-token")
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission(helper),
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
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetPrivateAPIToken("expected-token")
		helper.SetPrivateAPIUserAgent("HarukiProxy")
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission(helper),
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
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetPrivateAPIToken("expected-token")
		helper.SetPrivateAPIUserAgent("HarukiProxy")
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission(helper),
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

	t.Run("runtime token update takes effect immediately", func(t *testing.T) {
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
		helper.SetPrivateAPIToken("token-a")
		app := fiber.New()
		app.Get("/",
			ValidateUserPermission(helper),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) },
		)

		reqA := httptest.NewRequest(http.MethodGet, "/", nil)
		reqA.Header.Set("Authorization", "token-a")
		respA, err := app.Test(reqA)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respA.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", respA.StatusCode, fiber.StatusNoContent)
		}

		helper.SetPrivateAPIToken("token-b")

		reqOld := httptest.NewRequest(http.MethodGet, "/", nil)
		reqOld.Header.Set("Authorization", "token-a")
		respOld, err := app.Test(reqOld)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respOld.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", respOld.StatusCode, fiber.StatusUnauthorized)
		}

		reqNew := httptest.NewRequest(http.MethodGet, "/", nil)
		reqNew.Header.Set("Authorization", "token-b")
		respNew, err := app.Test(reqNew)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if respNew.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", respNew.StatusCode, fiber.StatusNoContent)
		}
	})
}

func TestProcessRequestKeys(t *testing.T) {
	base := bson.D{
		{Key: "a", Value: float64(1)},
		{Key: "b", Value: "two"},
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

func TestMapPrivateGameAccountLookupError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if got := mapPrivateGameAccountLookupError(nil); got != nil {
			t.Fatalf("mapPrivateGameAccountLookupError(nil) = %#v, want nil", got)
		}
	})

	t.Run("not found", func(t *testing.T) {
		got := mapPrivateGameAccountLookupError(&postgresql.NotFoundError{})
		if got == nil {
			t.Fatalf("expected non-nil error")
		}
		if got.Code != fiber.StatusNotFound {
			t.Fatalf("status = %d, want %d", got.Code, fiber.StatusNotFound)
		}
		if got.Message != "account binding not found" {
			t.Fatalf("message = %q", got.Message)
		}
	})

	t.Run("internal", func(t *testing.T) {
		got := mapPrivateGameAccountLookupError(assertErr{})
		if got == nil {
			t.Fatalf("expected non-nil error")
		}
		if got.Code != fiber.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", got.Code, fiber.StatusInternalServerError)
		}
		if got.Message != "failed to query game account" {
			t.Fatalf("message = %q", got.Message)
		}
	})
}

func TestMapPrivateAuthorizationLookupError(t *testing.T) {
	if got := mapPrivateAuthorizationLookupError(nil); got != nil {
		t.Fatalf("mapPrivateAuthorizationLookupError(nil) = %#v, want nil", got)
	}
	got := mapPrivateAuthorizationLookupError(assertErr{})
	if got == nil {
		t.Fatalf("expected non-nil error")
	}
	if got.Code != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", got.Code, fiber.StatusInternalServerError)
	}
	if got.Message != "failed to verify authorization" {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestMapPrivateDataQueryError(t *testing.T) {
	if got := mapPrivateDataQueryError(nil); got != nil {
		t.Fatalf("mapPrivateDataQueryError(nil) = %#v, want nil", got)
	}
	got := mapPrivateDataQueryError(assertErr{})
	if got == nil {
		t.Fatalf("expected non-nil error")
	}
	if got.Code != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", got.Code, fiber.StatusInternalServerError)
	}
	if got.Message != "failed to query user data" {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestMapPrivateBindingOwnerError(t *testing.T) {
	t.Run("nil binding", func(t *testing.T) {
		got := mapPrivateBindingOwnerError(nil)
		if got == nil {
			t.Fatalf("expected non-nil error")
		}
		if got.Code != fiber.StatusNotFound {
			t.Fatalf("status = %d, want %d", got.Code, fiber.StatusNotFound)
		}
		if got.Message != "account binding not found" {
			t.Fatalf("message = %q", got.Message)
		}
	})

	t.Run("missing owner", func(t *testing.T) {
		got := mapPrivateBindingOwnerError(&postgresql.GameAccountBinding{})
		if got == nil {
			t.Fatalf("expected non-nil error")
		}
		if got.Code != fiber.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", got.Code, fiber.StatusInternalServerError)
		}
		if got.Message != "failed to query game account owner" {
			t.Fatalf("message = %q", got.Message)
		}
	})

	t.Run("banned owner", func(t *testing.T) {
		binding := &postgresql.GameAccountBinding{}
		binding.Edges.User = &postgresql.User{Banned: true}
		got := mapPrivateBindingOwnerError(binding)
		if got == nil {
			t.Fatalf("expected non-nil error")
		}
		if got.Code != fiber.StatusForbidden {
			t.Fatalf("status = %d, want %d", got.Code, fiber.StatusForbidden)
		}
		if got.Message != "forbidden: account owner is banned" {
			t.Fatalf("message = %q", got.Message)
		}
	})

	t.Run("valid owner", func(t *testing.T) {
		binding := &postgresql.GameAccountBinding{}
		binding.Edges.User = &postgresql.User{Banned: false}
		if got := mapPrivateBindingOwnerError(binding); got != nil {
			t.Fatalf("expected nil, got %#v", got)
		}
	})
}

type assertErr struct{}

func (assertErr) Error() string { return "assert" }
