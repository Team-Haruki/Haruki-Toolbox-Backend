package upload

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	config "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	redisManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/gofiber/fiber/v3"
)

func TestIOSCodeWriteGrantAndQueuedRevocation(t *testing.T) {
	previous := config.Cfg.SekaiClient
	t.Cleanup(func() { config.Cfg.SekaiClient = previous })
	config.Cfg.SekaiClient = config.SekaiClientConfig{JPServerAPIHost: "jp.example.test", ENServerAPIHost: "en.example.test", TWServerAPIHost: "tw.example.test", KRServerAPIHost: "kr.example.test", CNServerAPIHost: "cn.example.test"}
	// The queued runner holds the upload until the test runs it, so the final
	// chunk stops waiting and answers 202 (received, still processing).
	previousWait := scriptUploadAckWait
	scriptUploadAckWait = 20 * time.Millisecond
	t.Cleanup(func() { scriptUploadAckWait = previousWait })
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
			if resp.StatusCode != 202 {
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

func TestIOSScriptBudgetDoesNotUseTheSharedProxyAddress(t *testing.T) {
	db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "script-quota"))
	defer db.Close()
	ctx := t.Context()
	for _, id := range []string{"alice", "bob"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").ExecX(ctx)
		db.IOSScriptCode.Create().SetUserID(id).SetUploadCode("fixture-" + id).ExecX(ctx)
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: &redisManager.HarukiRedisManager{Redis: newIOSUploadRedisClient(t)}}}
	for _, at := range []time.Time{time.Now().UTC(), time.Now().UTC().Add(time.Minute)} {
		for range openUploadRateLimitPerMinute {
			if _, _, err := consumeOpenUploadRateLimit(ctx, helper, iosScriptUploadRateBucket("alice"), at); err != nil {
				t.Fatal(err)
			}
		}
	}
	app := fiber.New()
	helper.Router = app
	registerIOSUploadRoutes(helper, testUploadDependencies())
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/ios/script/fixture-alice/upload", fiber.StatusTooManyRequests},
		{"/ios/script/fixture-alice/upload", fiber.StatusTooManyRequests},
		{"/api/ios/script/fixture-bob/upload", fiber.StatusBadRequest},
		{"/api/ios/script/unknown/upload", fiber.StatusUnauthorized},
	} {
		resp, err := app.Test(httptest.NewRequest(fiber.MethodPost, tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("%s returned %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
		if resp.Header.Get(proxyUploadResultHeader) != "" {
			t.Fatal("an authenticated script response carried proxy headers")
		}
	}
}
