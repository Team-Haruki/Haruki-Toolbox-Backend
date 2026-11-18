package upload

import (
	"context"
	"crypto/sha256"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	config "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	redisManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/gofiber/fiber/v3"
)

type uploadAssemblyHook func()

func (beforeAssembly uploadAssemblyHook) Go(_ string, task func()) bool {
	beforeAssembly()
	task()
	return true
}

type uploadFunc = func(context.Context, []byte, harukiUtils.SupportedDataUploadServer, harukiUtils.UploadDataType, *int64, *string, *api.HarukiToolboxRouterHelpers, Dependencies, harukiUtils.UploadMethod, ...*platform.Attempt) (*harukiUtils.HandleDataResult, error)

func stubRunUpload(t *testing.T, fn uploadFunc) {
	t.Helper()
	previous := runUpload
	runUpload = fn
	t.Cleanup(func() { runUpload = previous })
}

// failingUpload fails the way HandleUpload does: it classifies the attempt.
func failingUpload(stage string, err error) uploadFunc {
	return func(_ context.Context, _ []byte, _ harukiUtils.SupportedDataUploadServer, _ harukiUtils.UploadDataType, _ *int64, _ *string, _ *api.HarukiToolboxRouterHelpers, _ Dependencies, _ harukiUtils.UploadMethod, attempts ...*platform.Attempt) (*harukiUtils.HandleDataResult, error) {
		classifyAttemptFailure(attempts[0], stage, err)
		return nil, err
	}
}

func succeedingUpload(_ context.Context, _ []byte, _ harukiUtils.SupportedDataUploadServer, _ harukiUtils.UploadDataType, gameUserID *int64, _ *string, _ *api.HarukiToolboxRouterHelpers, _ Dependencies, _ harukiUtils.UploadMethod, _ ...*platform.Attempt) (*harukiUtils.HandleDataResult, error) {
	return &harukiUtils.HandleDataResult{UserID: gameUserID}, nil
}

// ★ The final chunk of a script upload answers with the processing result, so
// the module's notification reports a payload the server could not use
// instead of claiming success; a slow upload answers 202 instead of holding
// the game's response.
func TestIOSScriptUploadAnswersWithTheProcessingResult(t *testing.T) {
	previous := config.Cfg.SekaiClient
	t.Cleanup(func() { config.Cfg.SekaiClient = previous })
	config.Cfg.SekaiClient = config.SekaiClientConfig{JPServerAPIHost: "jp.example.test", ENServerAPIHost: "en.example.test", TWServerAPIHost: "tw.example.test", KRServerAPIHost: "kr.example.test", CNServerAPIHost: "cn.example.test"}
	previousWait := scriptUploadAckWait
	t.Cleanup(func() { scriptUploadAckWait = previousWait })

	db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "script-result"))
	defer db.Close()
	ctx := context.Background()
	if _, err := db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.com").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	record := db.IOSScriptCode.Create().SetUserID("owner").SetUploadCode("owner-code").SaveX(ctx)
	keyFor := func(id string) string {
		return buildChunkUploadKey("owner", harukiUtils.SupportedDataUploadServerJP, 123,
			fmt.Sprintf("%d:%x:%s:%s", record.ID, sha256.Sum256([]byte(record.UploadCode)), harukiUtils.UploadDataTypeSuite, id))
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: &redisManager.HarukiRedisManager{Redis: newIOSUploadRedisClient(t)}}}

	post := func(t *testing.T, d Dependencies, uploadID string) (int, string) {
		t.Helper()
		app := fiber.New()
		app.Post("/:upload_code", handleIOSScriptUploadWithValidation(helper, d, d.DataHandlerLogger))
		req := httptest.NewRequest("POST", "/owner-code", strings.NewReader("payload"))
		req.Header.Set("X-Chunk-Index", "0")
		req.Header.Set("X-Total-Chunks", "1")
		req.Header.Set("X-Upload-Id", uploadID)
		req.Header.Set("X-Original-Url", "https://jp.example.test/api/suite/user/123")
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == fiber.StatusTooManyRequests {
			if resp.Header.Get(fiber.HeaderRetryAfter) == "" {
				t.Fatal("retryable upload response omitted Retry-After")
			}
			return resp.StatusCode, string(raw)
		}
		var body struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		return resp.StatusCode, body.Message
	}

	t.Run("assembly capacity permits retry of the same upload", func(t *testing.T) {
		stubRunUpload(t, succeedingUpload)
		d := testUploadDependencies()
		d.BackgroundTasks = background.InlineRunner{}
		func() {
			for range cap(iosUploadAssemblySlots) {
				iosUploadAssemblySlots <- struct{}{}
				defer func() { <-iosUploadAssemblySlots }()
			}
			if status, _ := post(t, d, "capacity"); status != fiber.StatusTooManyRequests {
				t.Fatalf("busy assembly returned %d", status)
			}
		}()
		if status, _ := post(t, d, "capacity"); status != fiber.StatusOK {
			t.Fatalf("assembly retry returned %d", status)
		}
	})
	t.Run("shutdown refuses admission and leaves the upload retryable", func(t *testing.T) {
		stubRunUpload(t, succeedingUpload)
		d := testUploadDependencies()
		d.BackgroundTasks = &queuedUploadRunner{accept: false}
		if status, _ := post(t, d, "shutting-down"); status != fiber.StatusTooManyRequests {
			t.Fatalf("draining worker returned %d", status)
		}
		d.BackgroundTasks = background.InlineRunner{}
		if status, _ := post(t, d, "shutting-down"); status != fiber.StatusOK {
			t.Fatalf("assembly on retry returned %d", status)
		}
	})
	t.Run("competing claim is retryable and remains owned by its worker", func(t *testing.T) {
		stubRunUpload(t, succeedingUpload)
		d := testUploadDependencies()
		d.BackgroundTasks = background.InlineRunner{}
		const uploadID = "existing-claim"
		key := keyFor(uploadID)
		redis := helper.DBManager.Redis.Redis
		if _, err := persistIOSUploadChunk(ctx, redis, key, 1, 0, []byte("payload")); err != nil {
			t.Fatal(err)
		}
		if ok, err := claimIOSUploadChunks(ctx, redis, key, 1, "other-worker"); err != nil || !ok {
			t.Fatalf("claim = %t, %v", ok, err)
		}
		if status, _ := post(t, d, uploadID); status != fiber.StatusTooManyRequests {
			t.Fatalf("active claim returned %d", status)
		}
		_, _, claimKey := iosUploadRedisKeys(key)
		if got, err := redis.Get(ctx, claimKey).Result(); err != nil || got != "other-worker" {
			t.Fatalf("claim holder = %q, %v", got, err)
		}
		if err := resetIOSUploadClaim(ctx, redis, key, "other-worker"); err != nil {
			t.Fatal(err)
		}
		if status, _ := post(t, d, uploadID); status != fiber.StatusOK {
			t.Fatalf("recovered claim returned %d", status)
		}
	})
	t.Run("expired assembly claim leaves stored chunks retryable", func(t *testing.T) {
		stubRunUpload(t, succeedingUpload)
		d := testUploadDependencies()
		const uploadID = "expired-claim"
		_, _, claimKey := iosUploadRedisKeys(keyFor(uploadID))
		d.BackgroundTasks = uploadAssemblyHook(func() {
			if err := helper.DBManager.Redis.Redis.Del(ctx, claimKey).Err(); err != nil {
				t.Error(err)
			}
		})
		if status, _ := post(t, d, uploadID); status != fiber.StatusTooManyRequests {
			t.Fatalf("lost assembly claim returned %d", status)
		}
		d.BackgroundTasks = background.InlineRunner{}
		if status, _ := post(t, d, uploadID); status != fiber.StatusOK {
			t.Fatalf("assembly after lost claim returned %d", status)
		}
	})
	t.Run("storage budget refuses within the upload size limit", func(t *testing.T) {
		redis := helper.DBManager.Redis.Redis
		budget := redisManager.BuildIOSUploadChunkBudgetKey()
		if err := redis.HSet(ctx, budget, "owner:owner", iosChunkPerOwnerBudget).Err(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = redis.HDel(ctx, budget, "owner:owner").Err() }()
		d := testUploadDependencies()
		if status, _ := post(t, d, "storage-budget"); status != fiber.StatusTooManyRequests {
			t.Fatalf("storage budget returned %d", status)
		}
	})
	t.Run("an undecodable payload fails in the real pipeline", func(t *testing.T) {
		d := testUploadDependencies()
		d.BackgroundTasks = background.InlineRunner{}
		if status, message := post(t, d, "real"); status != fiber.StatusBadRequest || message != "invalid_upload_payload" {
			t.Fatalf("got %d %q", status, message)
		}
	})
	t.Run("a processed upload succeeds", func(t *testing.T) {
		stubRunUpload(t, succeedingUpload)
		d := testUploadDependencies()
		d.BackgroundTasks = background.InlineRunner{}
		if status, message := post(t, d, "ok"); status != fiber.StatusOK || message != "Successfully uploaded data." {
			t.Fatalf("got %d %q", status, message)
		}
	})
	t.Run("a refused upload is reported", func(t *testing.T) {
		stubRunUpload(t, failingUpload(uploadStageAccountPolicy, errUploadOwnershipMismatch))
		d := testUploadDependencies()
		d.BackgroundTasks = background.InlineRunner{}
		if status, message := post(t, d, "refused"); status != fiber.StatusForbidden || message != "upload_not_allowed" {
			t.Fatalf("got %d %q", status, message)
		}
	})
	t.Run("a slow upload answers 202 and finishes", func(t *testing.T) {
		release, finished := make(chan struct{}), make(chan struct{})
		stubRunUpload(t, func(ctx context.Context, data []byte, server harukiUtils.SupportedDataUploadServer, dataType harukiUtils.UploadDataType, gameUserID *int64, userID *string, h *api.HarukiToolboxRouterHelpers, d Dependencies, m harukiUtils.UploadMethod, a ...*platform.Attempt) (*harukiUtils.HandleDataResult, error) {
			defer close(finished)
			<-release
			return succeedingUpload(ctx, data, server, dataType, gameUserID, userID, h, d, m, a...)
		})
		scriptUploadAckWait = 30 * time.Millisecond
		d := testUploadDependencies()
		d.BackgroundTasks = nil // a real goroutine
		if status, message := post(t, d, "slow"); status != fiber.StatusAccepted || message != "upload received, still processing" {
			t.Fatalf("got %d %q", status, message)
		}
		close(release)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("the upload did not finish after the response")
		}
	})
}

// ★ A proxy-mode response says how the upload went in X-Haruki-Upload-Result
// (and X-Haruki-Upload-Error), for the module's notification script, and
// never anything beyond the public error code.
func TestIOSProxyUploadReportsTheResultInHeaders(t *testing.T) {
	previousCall := proxyCallAPI
	t.Cleanup(func() { proxyCallAPI = previousCall })
	upstreamFails := false
	proxyCallAPI = func(_ context.Context, _ map[string]string, _ string, _ harukiUtils.SupportedDataUploadServer, _ harukiUtils.UploadDataType, _ []byte, _ map[string]string, _ string, _ int64, _ *int64) (*harukiUtils.SekaiDataRetrieverResponse, error) {
		if upstreamFails {
			return nil, errors.New("dial tcp 10.0.0.1:443: connection refused")
		}
		return &harukiUtils.SekaiDataRetrieverResponse{RawBody: []byte("game body"), StatusCode: 200, NewHeaders: map[string][]string{"X-Game": {"1"}}}, nil
	}
	helper := &api.HarukiToolboxRouterHelpers{}
	d := testUploadDependencies()
	app := fiber.New()
	app.Get("/ios/proxy/:server/suite/user/:user_id", HandleProxyUpload("", harukiUtils.UploadDataTypeSuite, helper, d, nil))

	for _, tc := range []struct {
		name       string
		upload     uploadFunc
		upstream   bool
		wantStatus int
		wantResult string
		wantError  string
		wantBody   string
	}{
		{"processed", succeedingUpload, false, 200, "ok", "", "game body"},
		{"invalid payload", failingUpload(uploadStageDecodePayload, errors.New("decode")), false, 500, "failed", "invalid_upload_payload", ""},
		{"not allowed", failingUpload(uploadStageAccountPolicy, errUploadOwnerBanned), false, 403, "failed", "upload_not_allowed", ""},
		{"game server unreachable", succeedingUpload, true, 500, "failed", "game_server_unavailable", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubRunUpload(t, tc.upload)
			upstreamFails = tc.upstream
			resp, err := app.Test(httptest.NewRequest("GET", "/ios/proxy/jp/suite/user/123", nil))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.wantStatus, raw)
			}
			if got := resp.Header.Get(proxyUploadResultHeader); got != tc.wantResult {
				t.Errorf("%s = %q, want %q", proxyUploadResultHeader, got, tc.wantResult)
			}
			if got := resp.Header.Get(proxyUploadErrorHeader); got != tc.wantError {
				t.Errorf("%s = %q, want %q", proxyUploadErrorHeader, got, tc.wantError)
			}
			if tc.wantBody != "" && (string(raw) != tc.wantBody || resp.Header.Get("X-Game") != "1") {
				t.Errorf("the game's response was not passed through: %q", raw)
			}
			if strings.Contains(string(raw), "10.0.0.1") {
				t.Errorf("the response leaks the upstream error: %s", raw)
			}
		})
	}
}

// ★ A proxy upload refused by the rate limiter tells the module's
// notification script (failed, rate_limited); the 429 itself, with its
// Retry-After, is unchanged, and other routes behind the same limiter do not
// get the upload headers.
func TestRateLimitedProxyUploadReportsTheResult(t *testing.T) {
	helper := newTestAPIHelperWithRedis(t)
	app := fiber.New()
	helper.Router = app
	registerIOSUploadRoutes(helper, testUploadDependencies())
	app.Post("/api/inherit/:server/:upload_type/submit", openUploadEntryGuard(helper), handleInheritSubmitGone)

	routes := []struct {
		method, pattern, path string
		wantHeaders           bool
	}{
		{"GET", "/ios/proxy/:server/suite/user/:user_id", "/ios/proxy/jp/suite/user/1", true},
		{"POST", "/api/ios/proxy/:server/user/:user_id/mysekai", "/api/ios/proxy/jp/user/1/mysekai", true},
		{"PUT", "/ios/proxy/:server/user/:user_id/mysekai/birthday-party/:party_id/delivery", "/ios/proxy/jp/user/1/mysekai/birthday-party/2/delivery", true},
		{"POST", "/api/inherit/:server/:upload_type/submit", "/api/inherit/jp/suite/submit", false},
	}
	// Use up this window and the next for each route, so a minute boundary
	// during the test cannot let a request through.
	now := time.Now().UTC()
	for _, r := range routes {
		key := "0.0.0.0|" + r.method + "|" + r.pattern
		for _, at := range []time.Time{now, now.Add(openUploadRateLimitWindow)} {
			for range openUploadRateLimitPerMinute {
				if _, _, err := consumeOpenUploadRateLimit(context.Background(), helper, key, at); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest(r.method, r.path, strings.NewReader("x")))
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != fiber.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
				t.Fatalf("status %d, Retry-After %q: want a 429 with Retry-After", resp.StatusCode, resp.Header.Get("Retry-After"))
			}
			result, code := resp.Header.Get(proxyUploadResultHeader), resp.Header.Get(proxyUploadErrorHeader)
			if r.wantHeaders && (result != "failed" || code != "rate_limited") {
				t.Errorf("upload headers = %q %q, want failed rate_limited", result, code)
			}
			if !r.wantHeaders && (result != "" || code != "") {
				t.Errorf("a non-proxy route carries upload headers %q %q", result, code)
			}
		})
	}
}
