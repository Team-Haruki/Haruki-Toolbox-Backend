package admincontent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminFriendLinkPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseAdminFriendLinkPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(payload.Name + "|" + payload.Description + "|" + payload.Avatar + "|" + payload.URL + "|" + strings.Join(payload.Tags, ","))
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"友情链接","description":"desc","avatar":"a.png","url":"https://example.com","tags":[" tech ","","tech","go"]}`))
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
		if string(body) != "友情链接|desc|a.png|https://example.com|tech,go" {
			t.Fatalf("response body = %q", string(body))
		}
	})

	t.Run("missing name", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"description":"desc","url":"https://example.com"}`))
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

func TestParseAdminFriendGroupPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseAdminFriendGroupPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(payload.Group)
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"group":"  工具站  "}`))
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
		if string(body) != "工具站" {
			t.Fatalf("response body = %q, want 工具站", string(body))
		}
	})

	t.Run("empty group", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"group":" "}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("accept name alias", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"  推荐群聊  "}`))
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
		if string(body) != "推荐群聊" {
			t.Fatalf("response body = %q, want 推荐群聊", string(body))
		}
	})
}

func TestParseAdminFriendGroupItemPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseAdminFriendGroupItemPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		avatar := "-"
		bg := "-"
		if payload.Avatar != nil {
			avatar = *payload.Avatar
		}
		if payload.Bg != nil {
			bg = *payload.Bg
		}
		return c.SendString(payload.Name + "|" + avatar + "|" + bg + "|" + payload.GroupInfo + "|" + payload.Detail)
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"A","avatar":"  aa.png ","bg":"", "groupInfo":"GI","detail":"detail"}`))
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
		if string(body) != "A|aa.png||GI|detail" {
			t.Fatalf("response body = %q, want %q", string(body), "A|aa.png||GI|detail")
		}
	})

	t.Run("missing required", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"A","groupInfo":"","detail":"detail"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("accept snake and description aliases", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"A","group_info":"GI","description":"detail"}`))
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
		if string(body) != "A|-|-|GI|detail" {
			t.Fatalf("response body = %q, want %q", string(body), "A|-|-|GI|detail")
		}
	})
}
