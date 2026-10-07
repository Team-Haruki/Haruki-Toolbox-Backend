package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"

	"github.com/gofiber/fiber/v3"
)

const testInternalAPIToken = "internal-api-route-test-token"

func testInternalAPIConfig(t *testing.T) oauth2Module.InternalAPIConfig {
	t.Helper()
	sum := sha256.Sum256([]byte(testInternalAPIToken))
	cfg, err := oauth2Module.ParseInternalAPIConfig(oauth2Module.InternalAPISettings{
		TokenSHA256: hex.EncodeToString(sum[:]),
		ClientID:    "station",
		Audience:    []string{"station"},
	})
	if err != nil {
		t.Fatalf("ParseInternalAPIConfig: %v", err)
	}
	return cfg
}

func newRoutesApp(t *testing.T, internalAPI oauth2Module.InternalAPIConfig) *fiber.App {
	t.Helper()
	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		Router:         app,
		DBManager:      &database.HarukiToolboxDBManager{},
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, ""),
	}
	RegisterRoutes(apiHelper, Dependencies{
		HydraConfig:       harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{}),
		OAuth2InternalAPI: internalAPI,
	})
	return app
}

// The subscription module's private API guard must not sit in front of the
// introspection route: its own internal token decides, so a missing token gets
// the internal API's 401 body, not the private API's.
func TestInternalIntrospectNotShadowedByPrivateAPIGuard(t *testing.T) {
	app := newRoutesApp(t, testInternalAPIConfig(t))

	req := httptest.NewRequest(http.MethodPost, oauth2Module.InternalIntrospectPath, strings.NewReader("token=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized || strings.TrimSpace(string(body)) != `{"error":"unauthorized"}` {
		t.Fatalf("status %d body %s, want 401 from the internal API itself", resp.StatusCode, body)
	}

	// The birthday-monitor routes keep their private API guard.
	req = httptest.NewRequest(http.MethodGet, "/internal/mysekai-birthday-events/e1", nil)
	req.Header.Set("Authorization", "Bearer "+testInternalAPIToken)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("private API route answered 200 without the private API token")
	}
}

func TestInternalIntrospectDisabledWithoutToken(t *testing.T) {
	app := newRoutesApp(t, oauth2Module.InternalAPIConfig{})
	for _, route := range app.GetRoutes(true) {
		if strings.HasPrefix(route.Path, "/internal/oauth2") {
			t.Fatalf("%s %s registered without oauth2.internal_api.token_sha256", route.Method, route.Path)
		}
	}

	req := httptest.NewRequest(http.MethodPost, oauth2Module.InternalIntrospectPath, strings.NewReader("token=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+testInternalAPIToken)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404 while the internal API is not configured", resp.StatusCode)
	}
}
