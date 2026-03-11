package userpasswordreset

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestResetPasswordHandlersRejectInvalidPayload(t *testing.T) {
	t.Parallel()

	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	app := fiber.New()
	app.Post("/api/user/reset-password/send", handleSendResetPassword(apiHelper))
	app.Post("/api/user/reset-password", handleResetPassword(apiHelper))

	invalidJSON := strings.NewReader("{")

	req := httptest.NewRequest(fiber.MethodPost, "/api/user/reset-password/send", invalidJSON)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test send returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("send status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}

	req = httptest.NewRequest(fiber.MethodPost, "/api/user/reset-password", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test reset returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("reset status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}
