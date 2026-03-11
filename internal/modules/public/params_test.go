package public

import (
	harukiUtils "haruki-suite/utils"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParseParams(t *testing.T) {
	app := fiber.New()
	app.Get("/public/:server/:data_type/:user_id", func(c fiber.Ctx) error {
		server, dataType, userID, userIDStr, err := parseParams(c)
		if err != nil {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		if server != harukiUtils.SupportedDataUploadServerJP || dataType != harukiUtils.UploadDataTypeSuite || userID != 123 || userIDStr != "123" {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/public/jp/suite/123", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/public/jp/suite/not-int", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}
