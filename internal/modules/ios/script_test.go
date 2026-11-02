package ios

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiDatabase "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

// The generated script falls back to its owner's module notification choice
// when an upload response does not carry one; a user who never chose gets the
// notifications.
func TestScriptGenerationBakesInTheModuleNotificationChoice(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-"))
	db := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	off := false
	for _, u := range []struct {
		id     string
		choice *bool
	}{{"never-chose", nil}, {"turned-off", &off}} {
		create := db.User.Create().SetID(u.id).SetName(u.id).SetEmail(u.id + "@example.com")
		if u.choice != nil {
			create = create.SetIosModuleNotificationsEnabled(*u.choice)
		}
		if _, err := create.Save(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.IOSScriptCode.Create().SetUserID(u.id).SetUploadCode(u.id + "-code").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &harukiDatabase.HarukiToolboxDBManager{DB: db}}
	app := fiber.New()
	app.Get("/:upload_code", handleScriptGeneration(helper, NewEndpointConfig(EndpointConfigOptions{BackendURL: "https://toolbox.example"})))

	for code, want := range map[string]string{"never-chose-code": "true", "turned-off-code": "false"} {
		resp, err := app.Test(httptest.NewRequest("GET", "/"+code+"?chunk=1", nil))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("%s: status %d: %s", code, resp.StatusCode, raw)
		}
		if line := "const notifyByDefault = " + want + ";"; !strings.Contains(string(raw), line) {
			t.Errorf("%s: script lacks %q", code, line)
		}
	}
}
