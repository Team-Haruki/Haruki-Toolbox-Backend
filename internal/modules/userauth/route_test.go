package userauth

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func newManagedIdentityHelper() *harukiAPIHelper.HarukiToolboxRouterHelpers {
	sessionHandler := harukiAPIHelper.NewSessionHandler(nil, "")
	sessionHandler.ConfigureIdentityProvider("kratos", "http://kratos.example", "http://kratos-admin.example", "", "", true, true, time.Second, nil)
	return &harukiAPIHelper.HarukiToolboxRouterHelpers{SessionHandler: sessionHandler}
}

func TestRegisterUserAuthRoutesDisablesLegacyEndpointsWhenManagedIdentityEnabled(t *testing.T) {
	app := fiber.New()
	helper := newManagedIdentityHelper()
	helper.Router = app
	RegisterUserAuthRoutes(helper)

	for _, path := range []string{"/api/user/login", "/api/user/register"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test(%s) returned error: %v", path, err)
		}
		if resp.StatusCode != fiber.StatusGone {
			t.Fatalf("%s status = %d, want %d", path, resp.StatusCode, fiber.StatusGone)
		}
	}
}
