package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestNormalizeAdminManageSocialPlatform(t *testing.T) {
	platform, err := normalizeAdminManageSocialPlatform(" qq ")
	if err != nil {
		t.Fatalf("normalizeAdminManageSocialPlatform returned error: %v", err)
	}
	if platform != "qq" {
		t.Fatalf("platform = %q, want qq", platform)
	}

	if _, err := normalizeAdminManageSocialPlatform("wecom"); err == nil {
		t.Fatalf("expected unsupported social platform to fail")
	}
}

func TestParseAdminManagedSocialPlatformPayload(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		payload, err := parseAdminManagedSocialPlatformPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		verified := "false"
		if payload.Verified != nil && *payload.Verified {
			verified = "true"
		}
		return c.SendString(payload.Platform + "," + payload.UserID + "," + verified)
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"platform":"discord","userId":"abc","verified":false}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "discord,abc,false" {
			t.Fatalf("response body = %q, want %q", string(body), "discord,abc,false")
		}
	})

	t.Run("snake case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"platform_name":"qqbot","user_id":"1001"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "qqbot,1001,true" {
			t.Fatalf("response body = %q, want %q", string(body), "qqbot,1001,true")
		}
	})

	t.Run("missing user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"platform":"qq"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminManagedAuthorizedSocialPayload(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		payload, err := parseAdminManagedAuthorizedSocialPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(payload.Platform + "," + payload.UserID + "," + payload.Comment)
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"platform":"telegram","userId":"1002","comment":"  hello "}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "telegram,1002,hello" {
			t.Fatalf("response body = %q, want %q", string(body), "telegram,1002,hello")
		}
	})

	t.Run("unsupported platform", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"platform":"line","userId":"1002"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestGenerateAdminIOSUploadCode(t *testing.T) {
	code, err := generateAdminIOSUploadCode()
	if err != nil {
		t.Fatalf("generateAdminIOSUploadCode returned error: %v", err)
	}
	if len(code) != 32 {
		t.Fatalf("len(code) = %d, want 32", len(code))
	}
}

func TestParseAdminManagedEmailPayload(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		payload, err := parseAdminManagedEmailPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		return c.SendString(payload.Email)
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"email":"test@example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "test@example.com" {
			t.Fatalf("response body = %q, want %q", string(body), "test@example.com")
		}
	})

	t.Run("missing email", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid email format", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"email":"not-an-email"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminUpdateAllowCNMysekaiPayload(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		payload, err := parseAdminUpdateAllowCNMysekaiPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		if payload.AllowCNMysekai != nil && *payload.AllowCNMysekai {
			return c.SendString("true")
		}
		return c.SendString("false")
	})

	t.Run("camel case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"allowCNMysekai":true}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "true" {
			t.Fatalf("response body = %q, want %q", string(body), "true")
		}
	})

	t.Run("snake case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"allow_cn_mysekai":false}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "false" {
			t.Fatalf("response body = %q, want %q", string(body), "false")
		}
	})

	t.Run("missing field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}
