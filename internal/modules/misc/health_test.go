package misc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"

	"github.com/gofiber/fiber/v3"
)

func TestHealthHandler(t *testing.T) {
	app := fiber.New()
	suiteRestoreService := harukiHandler.NewSuiteRestoreService(harukiHandler.SuiteRestoreServiceOptions{})
	app.Get("/health", handleHealth(nil, suiteRestoreService))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	status, ok := payload["status"].(string)
	if !ok {
		t.Fatalf("status field type = %T, want string", payload["status"])
	}
	if status != "ok" && status != "degraded" {
		t.Fatalf("status field = %#v, want %q or %q", status, "ok", "degraded")
	}
	if _, ok := payload["time"]; !ok {
		t.Fatalf("time field missing")
	}
	suiteRestorer, ok := payload["suiteRestorer"].(map[string]any)
	if !ok {
		t.Fatalf("suiteRestorer field missing or invalid: %#v", payload["suiteRestorer"])
	}
	if _, ok := suiteRestorer["loadedRegions"]; !ok {
		t.Fatalf("suiteRestorer.loadedRegions missing")
	}
	if _, ok := suiteRestorer["failedRegions"]; !ok {
		t.Fatalf("suiteRestorer.failedRegions missing")
	}
}

func TestHealthHandlerUsesDefensiveSuiteRestoreStatus(t *testing.T) {
	suiteRestoreService := harukiHandler.NewSuiteRestoreService(harukiHandler.SuiteRestoreServiceOptions{
		StructuresFile: map[string]string{
			"en": filepath.Join(t.TempDir(), "missing.avsc"),
		},
	})
	app := fiber.New()
	app.Get("/health", handleHealth(nil, suiteRestoreService))

	_, failures := suiteRestoreService.LoadStatus()
	failures["en"] = "mutated outside service"

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if payload["status"] != "degraded" {
		t.Fatalf("status = %#v, want degraded", payload["status"])
	}
	suiteRestorer, ok := payload["suiteRestorer"].(map[string]any)
	if !ok {
		t.Fatalf("suiteRestorer field missing or invalid: %#v", payload["suiteRestorer"])
	}
	failedRegions, ok := suiteRestorer["failedRegions"].(map[string]any)
	if !ok {
		t.Fatalf("failedRegions field missing or invalid: %#v", suiteRestorer["failedRegions"])
	}
	if failedRegions["en"] == "mutated outside service" {
		t.Fatal("health response observed mutation of a previously returned status map")
	}
}
