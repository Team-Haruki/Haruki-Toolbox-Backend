package ios

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	iosGen "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/ios"
	harukiDatabase "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

// The notify query parameter of module and script URLs sets the default of
// upload notifications; without it they are on.
func TestNotifyParameterSetsTheModuleDefault(t *testing.T) {
	original := harukiConfig.Cfg
	t.Cleanup(func() { harukiConfig.Cfg = original })
	harukiConfig.Cfg.SekaiClient.JPServerAPIHost = "jp.example.com"

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-"))
	db := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.User.Create().SetID("u").SetName("u").SetEmail("u@example.com").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.IOSScriptCode.Create().SetUserID("u").SetUploadCode("code").Save(ctx); err != nil {
		t.Fatal(err)
	}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
	endpoints := NewEndpointConfig(EndpointConfigOptions{BackendURL: "https://toolbox.example"})
	app := fiber.New()
	app.Get("/module/:upload_code/*", handleModuleGeneration(helper, endpoints))
	app.Get("/script/proxy-notify.js", handleProxyNotifyScript())
	app.Get("/script/:upload_code", handleScriptGeneration(helper, endpoints))

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(raw)
	}

	for query, want := range map[string]string{"": "true", "&notify=true": "true", "&notify=false": "false", "&notify=0": "false"} {
		status, body := get("/module/code/jp-haruki-toolbox-suite.sgmodule?mode=script" + query)
		if status != fiber.StatusOK || !strings.Contains(body, "#!arguments=notify:"+want+"\n") {
			t.Errorf("module%s: status %d, want #!arguments=notify:%s in\n%s", query, status, want, body)
		}
		status, body = get("/script/code?chunk=1" + query)
		if status != fiber.StatusOK || !strings.Contains(body, "const notifyByDefault = "+want+";") {
			t.Errorf("script%s: status %d, want default %s", query, status, want)
		}
	}
	for query, want := range map[string]string{"": "true", "?notify=false": "false"} {
		status, body := get("/script/proxy-notify.js" + query)
		if status != fiber.StatusOK || !strings.Contains(body, "const notifyByDefault = "+want+";") {
			t.Errorf("proxy notify script%s: status %d, want default %s", query, status, want)
		}
	}
	for _, path := range []string{"/module/code/jp-haruki-toolbox-suite.sgmodule?mode=script&notify=maybe", "/script/code?notify=maybe", "/script/proxy-notify.js?notify=maybe"} {
		if status, _ := get(path); status != fiber.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", path, status)
		}
	}
}

// The proxy notification script is served at the path the Loon plugin names,
// next to the per-code uploader script.
func TestProxyNotifyScriptRoute(t *testing.T) {
	app := fiber.New()
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app}
	RegisterIOSRoutes(helper, NewEndpointConfig(EndpointConfigOptions{BackendURL: "https://toolbox.example"}))
	for _, path := range []string{iosGen.ProxyNotifyScriptPath, "/api" + iosGen.ProxyNotifyScriptPath} {
		resp, err := app.Test(httptest.NewRequest("GET", path+"?v="+iosGen.ProxyNotifyScriptVersion, nil))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK || !strings.Contains(string(raw), "X-Haruki-Upload-Result") {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
	}
}
