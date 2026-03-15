package useremail

import (
	userauth "haruki-suite/internal/modules/userauth"
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestRegisterUserEmailRoutesDisablesLegacyEndpointsWhenManagedIdentityEnabled(t *testing.T) {
	app := fiber.New()
	sessionHandler := harukiAPIHelper.NewSessionHandler(nil, "")
	sessionHandler.ConfigureIdentityProvider("kratos", "http://kratos.example", "http://kratos-admin.example", "", "", true, true, time.Second, nil)
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, SessionHandler: sessionHandler}
	RegisterUserEmailRoutes(helper)

	for _, path := range []string{"/api/email/send", "/api/email/verify"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test(%s) returned error: %v", path, err)
		}
		if resp.StatusCode != fiber.StatusGone {
			t.Fatalf("%s status = %d, want %d", path, resp.StatusCode, fiber.StatusGone)
		}
	}

	if userauth.ManagedIdentityMessage == "" {
		t.Fatalf("expected managed identity message to be exported")
	}
}
