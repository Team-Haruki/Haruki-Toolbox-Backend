package misc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestHealthHandler(t *testing.T) {
	app := fiber.New()
	app.Get("/health", handleHealth())

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
