package userpasswordreset

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestResetPasswordHandlersRejectInvalidPayload(t *testing.T) {
	t.Parallel()

	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	app := fiber.New()
	app.Post("/api/user/reset-password/send", handleSendResetPassword(apiHelper, nil))
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

func TestHandleSendResetPasswordTreatsMissingVerifierAsUnavailable(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Post("/api/user/reset-password/send", handleSendResetPassword(&harukiAPIHelper.HarukiToolboxRouterHelpers{}, nil))

	req := httptest.NewRequest(fiber.MethodPost, "/api/user/reset-password/send", strings.NewReader(`{"email":"recover@example.com","challengeToken":"token"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusInternalServerError)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !strings.Contains(string(body), `"message":"captcha service unavailable"`) {
		t.Fatalf("body = %s, want captcha service unavailable", body)
	}
}
