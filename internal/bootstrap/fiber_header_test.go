package bootstrap

import (
	"net/http/httptest"
	"strings"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

	"github.com/gofiber/fiber/v3"
)

// A Hydra consent redirect carries a ~2.5 KiB challenge in the query, and the
// browser sends Kratos and Hydra CSRF cookies with it. Fiber's 4 KiB default
// read buffer answered 431 to such requests in production (2026-10-08).
func TestFiberAcceptsConsentSizedRequestHeaders(t *testing.T) {
	app, closeLog, err := newFiberApp(harukiConfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	app.Get("/api/oauth2/consent", func(c fiber.Ctx) error { return c.SendStatus(204) })

	for _, tc := range []struct {
		name          string
		query, cookie int
		want          int
	}{
		{"consent challenge plus session and csrf cookies", 3000, 6000, 204},
		{"beyond the configured buffer", requestHeaderBufferSize, requestHeaderBufferSize, 431},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/oauth2/consent?consent_challenge="+strings.Repeat("a", tc.query), nil)
			req.Header.Set("Cookie", "ory_kratos_session="+strings.Repeat("b", tc.cookie))
			resp, err := app.Test(req)
			if tc.want == 431 {
				// fasthttp rejects an oversized header block before routing; the
				// in-memory test transport surfaces that as an error, a real
				// connection as 431. Either way the request must not reach the handler.
				if err == nil && resp.StatusCode != 431 {
					t.Fatalf("status = %d, want the request rejected", resp.StatusCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}
