package adminusers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestSanitizeBatchUserIDs(t *testing.T) {
	t.Run("trim and dedupe", func(t *testing.T) {
		got, err := sanitizeBatchUserIDs([]string{" 1001 ", "1002", "1001", " "})
		if err != nil {
			t.Fatalf("sanitizeBatchUserIDs returned error: %v", err)
		}
		if len(got) != 2 || got[0] != "1001" || got[1] != "1002" {
			t.Fatalf("unexpected result: %#v", got)
		}
	})

	t.Run("empty rejected", func(t *testing.T) {
		if _, err := sanitizeBatchUserIDs([]string{" ", ""}); err == nil {
			t.Fatalf("expected error for empty userIds")
		}
	})

	t.Run("too many rejected", func(t *testing.T) {
		values := make([]string, 0, maxBatchUserOperationCount+1)
		for i := 0; i < maxBatchUserOperationCount+1; i++ {
			values = append(values, strconv.Itoa(100000+i))
		}
		if _, err := sanitizeBatchUserIDs(values); err == nil {
			t.Fatalf("expected error for too many userIds")
		}
	})
}

func TestParseBatchUserRoleUpdatePayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseBatchUserRoleUpdatePayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(payload.Role + "|" + strings.Join(payload.UserIDs, ","))
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"userIds":[" 1001 ","1002","1001"],"role":" admin "}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "admin|1001,1002" {
			t.Fatalf("body = %q, want %q", string(body), "admin|1001,1002")
		}
	})

	t.Run("missing role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"userIds":["1001"]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"userIds":["1001"],"role":"owner"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseBatchUserAllowCNMysekaiUpdatePayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseBatchUserAllowCNMysekaiUpdatePayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		allow := "false"
		if payload.AllowCNMysekai != nil && *payload.AllowCNMysekai {
			allow = "true"
		}
		return c.SendString(allow + "|" + strings.Join(payload.UserIDs, ","))
	})

	t.Run("camel case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"userIds":["1001","1002"],"allowCNMysekai":true}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "true|1001,1002" {
			t.Fatalf("body = %q, want %q", string(body), "true|1001,1002")
		}
	})

	t.Run("snake case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"userIds":["1001"],"allow_cn_mysekai":false}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "false|1001" {
			t.Fatalf("body = %q, want %q", string(body), "false|1001")
		}
	})

	t.Run("missing allow field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"userIds":["1001"]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}
