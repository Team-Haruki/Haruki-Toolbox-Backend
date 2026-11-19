package admincore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestAdminMutationRequiresExactFrontendOrigin(t *testing.T) {
	app := fiber.New()
	app.Post("/admin", RequireTrustedBrowserOrigin("https://admin.example.com:443/settings"), func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	for _, tc := range []struct {
		origins []string
		want    int
	}{
		{[]string{"https://admin.example.com"}, 204},
		{[]string{"https://other.example.com"}, 403},
		{[]string{"https://admin.example.com", "https://other.example.com"}, 403},
		{[]string{"null"}, 403},
		{nil, 403},
	} {
		req := httptest.NewRequest(http.MethodPost, "/admin", nil)
		for _, origin := range tc.origins {
			req.Header.Add("Origin", origin)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("origin %q returned %d, want %d", tc.origins, resp.StatusCode, tc.want)
		}
	}
}

func TestAdminOriginConfigurationDoesNotBlockSafeReads(t *testing.T) {
	for _, tc := range []struct {
		configured string
		origin     string
		method     string
		want       int
	}{
		{"http://ADMIN.example.test:80/settings", "http://admin.example.test", http.MethodPost, http.StatusNoContent},
		{"http://admin.example.test:8080", "http://admin.example.test:8080", http.MethodPost, http.StatusNoContent},
		{"https://admin.example.test:8443", "https://admin.example.test", http.MethodPost, http.StatusForbidden},
		{"", "", http.MethodGet, http.StatusNoContent},
		{"", "", http.MethodHead, http.StatusNoContent},
		{"", "", http.MethodOptions, http.StatusNoContent},
		{"", "https://admin.example.test", http.MethodPost, http.StatusForbidden},
	} {
		app := fiber.New()
		app.All("/admin", RequireTrustedBrowserOrigin(tc.configured), func(c fiber.Ctx) error {
			return c.SendStatus(http.StatusNoContent)
		})
		req := httptest.NewRequest(tc.method, "/admin", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s configured %q, origin %q returned %d, want %d", tc.method, tc.configured, tc.origin, resp.StatusCode, tc.want)
		}
	}
}
