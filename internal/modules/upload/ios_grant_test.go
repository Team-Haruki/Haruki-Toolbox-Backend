package upload

import (
	"context"
	config "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	redisManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/gofiber/fiber/v3"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIOSCodeWriteGrantAndQueuedRevocation(t *testing.T) {
	previous := config.Cfg.SekaiClient
	t.Cleanup(func() { config.Cfg.SekaiClient = previous })
	config.Cfg.SekaiClient = config.SekaiClientConfig{JPServerAPIHost: "jp.example.test", ENServerAPIHost: "en.example.test", TWServerAPIHost: "tw.example.test", KRServerAPIHost: "kr.example.test", CNServerAPIHost: "cn.example.test"}
	for _, scenario := range []string{"read", "write", "revoked-code", "revoked-grant"} {
		t.Run(scenario, func(t *testing.T) {
			db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, scenario))
			defer db.Close()
			ctx := context.Background()
			for _, id := range []string{"owner", "actor"} {
				if _, err := db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").Save(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
				t.Fatal(err)
			}
			code, err := db.IOSScriptCode.Create().SetUserID("actor").SetUploadCode("test-user-code").Save(ctx)
			if err != nil {
				t.Fatal(err)
			}
			permission := "write"
			if scenario == "read" {
				permission = "read"
			}
			if _, err := db.UpsertGameAccountDataGrant(ctx, "owner", "actor", "jp", "123", "suite", time.Now().Add(time.Hour), []string{permission}); err != nil {
				t.Fatal(err)
			}
			helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: &redisManager.HarukiRedisManager{Redis: newIOSUploadRedisClient(t)}}}
			d := testUploadDependencies()
			runner := &queuedUploadRunner{accept: true}
			d.BackgroundTasks = runner
			app := fiber.New()
			app.Post("/:upload_code", handleIOSScriptUploadWithValidation(helper, d, d.DataHandlerLogger))
			req := httptest.NewRequest("POST", "/test-user-code", strings.NewReader("invalid game payload"))
			req.Header.Set("X-Chunk-Index", "0")
			req.Header.Set("X-Total-Chunks", "1")
			req.Header.Set("X-Upload-Id", "test-upload")
			req.Header.Set("X-Original-Url", "https://jp.example.test/api/suite/user/123")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if scenario == "read" {
				if resp.StatusCode != 403 {
					t.Fatal(resp.StatusCode)
				}
				return
			}
			if resp.StatusCode != 200 {
				t.Fatal(resp.StatusCode)
			}
			if scenario == "revoked-code" {
				if err := db.IOSScriptCode.DeleteOneID(code.ID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "revoked-grant" {
				if _, err := db.DeleteGameAccountDataGrant(ctx, "owner", "actor", "jp", "123", "suite"); err != nil {
					t.Fatal(err)
				}
			}
			_, tasks := runner.snapshot()
			if len(tasks) != 1 {
				t.Fatal("missing assembly")
			}
			tasks[0]()
			row, err := db.UploadLog.Query().Only(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := "invalid_upload_payload"
			if strings.HasPrefix(scenario, "revoked") {
				want = "upload_not_allowed"
			}
			if row.ErrorCode == nil || *row.ErrorCode != want || row.ActorUserID == nil || *row.ActorUserID != "actor" {
				t.Fatalf("unexpected audit: %+v", row)
			}
		})
	}
}
