package upload

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	config "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	iosGen "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/ios"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	redisManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/gofiber/fiber/v3"
)

// ★ Every script upload response from a known upload code carries the owner's
// current module notification choice, and the uploader script recognises the
// endpoint's two failure messages by their text. A reworded message would
// quietly fall back to the bare "HTTP 403" in the notification.
func TestIOSScriptUploadReportsTheModuleNotificationChoice(t *testing.T) {
	previous := config.Cfg.SekaiClient
	t.Cleanup(func() { config.Cfg.SekaiClient = previous })
	config.Cfg.SekaiClient = config.SekaiClientConfig{JPServerAPIHost: "jp.example.test", ENServerAPIHost: "en.example.test", TWServerAPIHost: "tw.example.test", KRServerAPIHost: "kr.example.test", CNServerAPIHost: "cn.example.test"}

	db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "module-notify"))
	defer db.Close()
	ctx := context.Background()
	if _, err := db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.com").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.User.Create().SetID("quiet").SetName("quiet").SetEmail("quiet@example.com").SetIosModuleNotificationsEnabled(false).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	for user, code := range map[string]string{"owner": "owner-code", "quiet": "quiet-code"} {
		if _, err := db.IOSScriptCode.Create().SetUserID(user).SetUploadCode(code).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: &redisManager.HarukiRedisManager{Redis: newIOSUploadRedisClient(t)}}}
	d := testUploadDependencies()
	d.BackgroundTasks = &queuedUploadRunner{accept: true}
	app := fiber.New()
	app.Post("/:upload_code", handleIOSScriptUploadWithValidation(helper, d, d.DataHandlerLogger))
	script := iosGen.GenerateScript("owner-code", 1, "https://toolbox.example", true)

	for _, tc := range []struct {
		name, code   string
		wantStatus   int
		wantHeader   string
		matchMessage bool
	}{
		{"never chose", "owner-code", fiber.StatusOK, "1", false},
		// quiet has no upload right to owner's account: the refusal still says
		// that quiet does not want the notification.
		{"turned off", "quiet-code", fiber.StatusForbidden, "0", true},
		// No owner is known, so the script falls back to its own default.
		{"unknown code", "nope", fiber.StatusUnauthorized, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/"+tc.code, strings.NewReader("payload"))
			req.Header.Set("X-Chunk-Index", "0")
			req.Header.Set("X-Total-Chunks", "1")
			req.Header.Set("X-Upload-Id", "upload-"+tc.code)
			req.Header.Set("X-Original-Url", "https://jp.example.test/api/suite/user/123")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.wantStatus, raw)
			}
			if got := resp.Header.Get(moduleNotifyHeader); got != tc.wantHeader {
				t.Fatalf("%s = %q, want %q", moduleNotifyHeader, got, tc.wantHeader)
			}
			if !tc.matchMessage {
				return
			}
			var body struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(script, `reason === "`+body.Message+`"`) {
				t.Errorf("the uploader script does not recognise %q", body.Message)
			}
		})
	}
	if !strings.Contains(script, `headerValue(resp, "`+moduleNotifyHeader+`")`) {
		t.Errorf("the uploader script does not read %s", moduleNotifyHeader)
	}
}
