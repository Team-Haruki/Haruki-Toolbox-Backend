package userprofile

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestRegisterUserProfileRoutesDisablesChangePasswordWhenManagedIdentityEnabled(t *testing.T) {
	app := fiber.New()
	sessionHandler := harukiAPIHelper.NewSessionHandler(nil, "")
	sessionHandler.ConfigureIdentityProvider("kratos", "http://kratos.example", "http://kratos-admin.example", "", "", true, true, time.Second, nil)
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, SessionHandler: sessionHandler}
	RegisterUserProfileRoutes(helper)

	req := httptest.NewRequest(http.MethodPut, "/api/user/u-1/change-password", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusGone {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusGone)
	}
}
