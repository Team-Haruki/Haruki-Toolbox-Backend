package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

func TestRegisterAdminRoutesRunsSessionMiddlewareOncePerRequest(t *testing.T) {
	var whoamiCalls atomic.Int64
	kratos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/whoami" {
			http.NotFound(w, r)
			return
		}
		whoamiCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"session-1","active":true,"identity":{"id":"identity-1","traits":{}}}`)
	}))
	defer kratos.Close()

	sessionHandler := harukiAPIHelper.NewSessionHandler(nil, "")
	sessionHandler.ConfigureIdentityProvider(
		"kratos",
		kratos.URL,
		"",
		"X-Session-Token",
		"ory_kratos_session",
		false,
		false,
		time.Second,
		nil,
	)
	sessionHandler.KratosIdentityResolver = func(context.Context, string, string) (string, error) {
		return "admin-user-1", nil
	}

	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		Router:         app,
		SessionHandler: sessionHandler,
	}
	registerAdminRoutes(apiHelper, Dependencies{HydraConfig: harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{})})

	for _, method := range []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			before := whoamiCalls.Load()
			req := httptest.NewRequest(method, "/api/admin/__session_middleware_probe__", nil)
			req.Header.Set("X-Session-Token", "test-session")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test returned error: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusNotFound {
				t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
			}

			if got := whoamiCalls.Load() - before; got != 1 {
				t.Fatalf("session middleware calls = %d, want 1", got)
			}
		})
	}
}

func TestRouteManifest(t *testing.T) {
	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		Router:         app,
		DBManager:      &database.HarukiToolboxDBManager{},
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, ""),
	}
	RegisterRoutes(apiHelper, Dependencies{HydraConfig: harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{})})

	actual := routeManifest(app)
	goldenPath := filepath.Join("testdata", "routes.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, []byte(actual), 0o644); err != nil {
			t.Fatalf("update route manifest: %v", err)
		}
	}
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read route manifest: %v", err)
	}
	if actual != string(expected) {
		t.Fatalf(
			"registered route manifest changed; review the method/path diff and run UPDATE_GOLDEN=1 go test ./api -run TestRouteManifest after approving it\n\nexpected:\n%s\nactual:\n%s",
			expected,
			actual,
		)
	}
}

func routeManifest(app *fiber.App) string {
	entries := make(map[string]struct{})
	for _, route := range app.GetRoutes(true) {
		// Fiber automatically mirrors GET routes as HEAD routes. Those entries are
		// framework noise rather than an independently registered API contract.
		if route.Method == http.MethodHead {
			continue
		}
		entries[route.Method+" "+route.Path] = struct{}{}
	}

	manifest := make([]string, 0, len(entries))
	for entry := range entries {
		manifest = append(manifest, entry)
	}
	sort.Strings(manifest)
	return strings.Join(manifest, "\n") + "\n"
}
