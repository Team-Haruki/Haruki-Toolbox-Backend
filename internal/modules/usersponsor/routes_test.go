package usersponsor

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userinfo"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/gofiber/fiber/v3"
)

func TestSponsorRoutesDoNotInterceptCurrentUserOrUnrelatedRoutes(t *testing.T) {
	e := newEnv(t)
	e.db.User.UpdateOneID("alice").SetKratosIdentityID("identity-alice").ExecX(t.Context())
	e.db.GameAccountBinding.Create().SetServer("tw").SetGameUserID("12345").SetUserID("alice").SetVerified(true).SaveX(t.Context())
	app := fiber.New()
	e.h.api.Router = app
	e.h.api.SessionHandler = &api.SessionHandler{
		DBClient: e.db, AuthProxyEnabled: true,
		AuthProxyTrustedHeader: "X-Test-Proxy", AuthProxyTrustedValue: "test-proxy-secret",
		AuthProxySubjectHeader: "X-Test-Identity", AuthProxyEmailVerifiedHeader: "X-Test-Email-Verified",
	}
	// Match production order, including the real authentication middleware.
	RegisterRoutes(e.h.api, OAuthConfig{}, sponsor.AfdianConfig{})
	userinfo.RegisterUserInfoRoutes(e.h.api, api.NewUserDataBuilder(""))
	app.Get("/api/user/unrelated-public", func(c fiber.Ctx) error { return c.SendStatus(204) })

	for _, tc := range []struct {
		method, path, body string
		auth               bool
		want               int
		contains           string
	}{
		{"GET", "/api/user/me", "", true, 200, `"userId":"12345"`},
		{"GET", "/api/user/alice/get-settings", "", true, 200, `"server":"tw"`},
		{"GET", "/api/user/unrelated-public", "", false, 204, ""},
		{"GET", "/api/user/alice/afdian", "", true, 200, `"eligible":false`},
		{"GET", "/api/user/alice/sponsor-frames", "", true, 200, `"gameUserId":"12345"`},
		{"GET", "/api/user/me", "", false, 401, ""},
		{"GET", "/api/user/alice/afdian", "", false, 401, ""},
		{"GET", "/api/user/alice/sponsor-frames", "", false, 401, ""},
		{"GET", "/api/user/bob/afdian", "", true, 401, ""},
		{"GET", "/api/user/bob/sponsor-frames", "", true, 401, ""},
		{"POST", "/api/user/alice/afdian/verify-order", `{"orderNumber":"12345678901234567890"}`, true, 403, ""},
		{"PUT", "/api/user/alice/sponsor-frames/tw/12345", `{"frameId":""}`, false, 401, ""},
		{"DELETE", "/api/user/alice/afdian", "", false, 401, ""},
	} {
		t.Run(tc.method+tc.path+"/"+tc.body+"/"+tc.contains, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.auth {
				req.Header.Set("X-Test-Proxy", "test-proxy-secret")
				req.Header.Set("X-Test-Identity", "identity-alice")
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want || !strings.Contains(string(body), tc.contains) {
				t.Fatalf("got %d %s; want %d containing %q", resp.StatusCode, body, tc.want, tc.contains)
			}
		})
	}
}
