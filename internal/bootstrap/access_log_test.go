package bootstrap

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"

	"github.com/gofiber/fiber/v3"
)

func TestAccessLogRedactsCredentialPaths(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "access.log")
	var cfg harukiConfig.Config
	// Production uses ${path}; ${url} is included to prove query strings are
	// covered too if the format is ever changed.
	cfg.Backend.AccessLog = "${method} ${path} ${url} ${status}\n"
	cfg.Backend.AccessLogPath = logPath

	app := fiber.New()
	closeLog, err := configureAccessLog(app, cfg)
	if err != nil {
		t.Fatalf("configureAccessLog: %v", err)
	}
	app.All("/*", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	secrets := []string{
		"FAKEafdiansecret05", "FAKEuploadcode05", "FAKEmodulecode05", "FAKEtoken05",
		"BCDFGHJK", "MNPQ-RSTV", "FAKEwrappeddevicecode05",
	}
	for _, target := range []string{
		"/api/sponsor/afdian/callback/FAKEafdiansecret05",
		"/ios/script/FAKEuploadcode05/upload",
		"/api/ios/module/FAKEmodulecode05/haruki.sgmodule",
		"/oauth2/sessions/logout?id_token_hint=FAKEtoken05&state=keep",
		"/device?user_code=BCDFGHJK",
		"/api/oauth2/device/lookup?user_code=MNPQ-RSTV&client_id=keep",
		"/api/oauth2/token?device_code=hdc_FAKEwrappeddevicecode05&grant_type=keep",
	} {
		resp, err := app.Test(httptest.NewRequest(fiber.MethodPost, target, nil))
		if err != nil {
			t.Fatalf("request %s: %v", target, err)
		}
		_ = resp.Body.Close()
	}
	if err := closeLog(); err != nil {
		t.Fatalf("close access log: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read access log: %v", err)
	}
	out := string(raw)
	for _, secret := range secrets {
		if strings.Contains(out, secret) {
			t.Fatalf("access log leaked %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{
		"/api/sponsor/afdian/callback/<redacted>",
		"/ios/script/<redacted>/upload",
		"/api/ios/module/<redacted>/haruki.sgmodule",
		"id_token_hint=<redacted>&state=keep",
		"/device?user_code=<redacted>",
		"/api/oauth2/device/lookup?user_code=<redacted>&client_id=keep",
		"/api/oauth2/token?device_code=<redacted>&grant_type=keep",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("access log missing %q:\n%s", want, out)
		}
	}
}

// Transfer (inherit) credentials must not reach the access log whichever tag
// would carry them: a game-API-shaped /inherit/user/{id} path, query
// parameters, or a ${body} tag logging the inherit submit payload. Game user
// IDs in data paths are the resource key every public URL carries and stay
// readable.
func TestAccessLogRedactsInheritCredentials(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "access.log")
	var cfg harukiConfig.Config
	cfg.Backend.AccessLog = "${method} ${path} ${url} ${body}\n"
	cfg.Backend.AccessLogPath = logPath

	app := fiber.New()
	closeLog, err := configureAccessLog(app, cfg)
	if err != nil {
		t.Fatalf("configureAccessLog: %v", err)
	}
	app.All("/*", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	const (
		inheritID       = "FAKEinheritID0006"
		inheritPassword = "FAKEinheritPW0006"
		gameUserID      = "12345678901234567"
	)
	requests := []struct {
		target string
		body   string
	}{
		{"/api/inherit/user/" + inheritID + "?isExecuteInherit=False", ""},
		{"/api/inherit/jp/suite/submit?inherit_id=" + inheritID + "&inheritPassword=" + inheritPassword + "&keep=1", ""},
		{"/api/inherit/jp/mysekai/submit", `{"inherit_id":"` + inheritID + `","inherit_password":"` + inheritPassword + `"}`},
		{"/api/public/jp/suite/" + gameUserID, ""},
	}
	for _, r := range requests {
		req := httptest.NewRequest(fiber.MethodPost, r.target, strings.NewReader(r.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request %s: %v", r.target, err)
		}
		_ = resp.Body.Close()
	}
	if err := closeLog(); err != nil {
		t.Fatalf("close access log: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read access log: %v", err)
	}
	out := string(raw)
	for _, secret := range []string{inheritID, inheritPassword} {
		if strings.Contains(out, secret) {
			t.Fatalf("access log leaked %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{
		"/api/inherit/user/<redacted>?isExecuteInherit=False",
		"inherit_id=<redacted>&inheritPassword=<redacted>&keep=1",
		`{"inherit_id":"<redacted>","inherit_password":"<redacted>"}`,
		"/api/inherit/jp/mysekai/submit",
		"/api/public/jp/suite/" + gameUserID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("access log missing %q:\n%s", want, out)
		}
	}
}
