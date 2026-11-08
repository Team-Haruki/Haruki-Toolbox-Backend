package upload

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	config "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
)

const birthdayPartyTestGameUserID = int64(7485938475928374)

// Birthday party delivery responses, as the game sends them.
func birthdayPartyDelivery(refreshed any, withMaps bool) map[string]any {
	updated := map[string]any{}
	if withMaps {
		updated["userMysekaiHarvestMaps"] = []any{map[string]any{"mysekaiSiteId": 5, "userMysekaiSiteHarvestFixtures": []any{}}}
	}
	data := map[string]any{"updatedResources": updated}
	if refreshed != nil {
		data["isRefreshed"] = refreshed
	}
	return data
}

type birthdayPartyPipeline struct {
	db     *postgresql.Client
	helper *api.HarukiToolboxRouterHelpers
	deps   Dependencies
}

// newBirthdayPartyPipeline runs the real upload pipeline for an owner and
// their verified JP account. It has no game data store unless one is passed,
// so a write that gets that far fails at the persist stage.
func newBirthdayPartyPipeline(t *testing.T, store *gamedata.Service) *birthdayPartyPipeline {
	t.Helper()
	db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "birthday-party-skip"))
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.com").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID(strconv.FormatInt(birthdayPartyTestGameUserID, 10)).SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, GameData: store, Redis: newTestAPIHelperWithRedis(t).DBManager.Redis}}
	deps := testUploadDependencies()
	deps.BackgroundTasks = background.InlineRunner{}
	deps.DataSync = platform.DataSyncConfig{} // no third-party sync in tests
	return &birthdayPartyPipeline{db: db, helper: helper, deps: deps}
}

func (p *birthdayPartyPipeline) pack(t *testing.T, content map[string]any) []byte {
	t.Helper()
	raw, err := p.deps.ServerCryptor.Pack(content, harukiUtils.SupportedDataUploadServerJP)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (p *birthdayPartyPipeline) upload(t *testing.T, content map[string]any) (*platform.Attempt, error) {
	t.Helper()
	gameUserID, owner := birthdayPartyTestGameUserID, "owner"
	attempt := newAttempt(harukiUtils.UploadMethodManual, 0)
	_, err := HandleUpload(context.Background(), p.pack(t, content), harukiUtils.SupportedDataUploadServerJP, harukiUtils.UploadDataTypeMysekaiBirthdayParty,
		&gameUserID, &owner, p.helper, p.deps, harukiUtils.UploadMethodManual, attempt)
	return attempt, err
}

func (p *birthdayPartyPipeline) uploadLogs(t *testing.T) int {
	t.Helper()
	count, err := p.db.UploadLog.Query().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// ★ A birthday party delivery whose response did not refresh the harvest
// points is skipped on every upload path: not stored, not audited as a
// failure. A refreshed one goes on to the write, and a refreshed one without
// harvest maps is still a real error.
func TestBirthdayPartyUploadSkipsADeliveryThatDidNotRefresh(t *testing.T) {
	p := newBirthdayPartyPipeline(t, nil)

	for name, delivery := range map[string]map[string]any{
		"not refreshed":                  birthdayPartyDelivery(false, false),
		"no isRefreshed":                 birthdayPartyDelivery(nil, false),
		"isRefreshed is not a boolean":   birthdayPartyDelivery("true", false),
		"not refreshed, with stale maps": birthdayPartyDelivery(false, true),
	} {
		t.Run(name, func(t *testing.T) {
			before := p.uploadLogs(t)
			attempt, err := p.upload(t, delivery)
			reason, skipped := uploadSkipReason(err)
			if !skipped || reason != "birthday_party_not_refreshed" {
				t.Fatalf("err = %v, want a birthday_party_not_refreshed skip", err)
			}
			if attempt.ErrorCode != "" || attempt.FailureStage != "" {
				t.Fatalf("a skip was classified as a failure: %+v", attempt)
			}
			if after := p.uploadLogs(t); after != before {
				t.Fatalf("a skip wrote %d upload logs", after-before)
			}
		})
	}

	t.Run("refreshed with harvest maps goes on to the write", func(t *testing.T) {
		attempt, err := p.upload(t, birthdayPartyDelivery(true, true))
		if _, skipped := uploadSkipReason(err); skipped || err == nil {
			t.Fatalf("err = %v, want the write to fail without a game data store", err)
		}
		if attempt.FailureStage != uploadStagePersist {
			t.Fatalf("failed at %q, want %q", attempt.FailureStage, uploadStagePersist)
		}
	})

	t.Run("refreshed without harvest maps is an error", func(t *testing.T) {
		before := p.uploadLogs(t)
		attempt, err := p.upload(t, birthdayPartyDelivery(true, false))
		if _, skipped := uploadSkipReason(err); skipped || err == nil {
			t.Fatalf("err = %v, want a preprocess error", err)
		}
		if attempt.FailureStage != uploadStagePreprocess || attempt.ErrorCode != "invalid_upload_payload" || attempt.HTTPStatus != fiber.StatusBadRequest {
			t.Fatalf("attempt = %+v, want 400 invalid_upload_payload at preprocess", attempt)
		}
		if after := p.uploadLogs(t); after != before+1 {
			t.Fatalf("the failure wrote %d upload logs, want 1", after-before)
		}
	})

	t.Run("the account policy still comes first", func(t *testing.T) {
		gameUserID, stranger := birthdayPartyTestGameUserID, "stranger"
		attempt := newAttempt(harukiUtils.UploadMethodManual, 0)
		_, err := HandleUpload(context.Background(), p.pack(t, birthdayPartyDelivery(false, false)), harukiUtils.SupportedDataUploadServerJP, harukiUtils.UploadDataTypeMysekaiBirthdayParty,
			&gameUserID, &stranger, p.helper, p.deps, harukiUtils.UploadMethodManual, attempt)
		if !errors.Is(err, errUploadOwnershipMismatch) {
			t.Fatalf("err = %v, want errUploadOwnershipMismatch", err)
		}
	})
}

// The same three cases against a real game data store: only the refreshed
// delivery with harvest maps is stored, and neither a skip nor an error
// touches what is already there.
//
//	GAMEDATA_HANDLER_TEST_PG=postgres://...   (a database this test may create tables in)
func TestBirthdayPartyUploadStoresOnlyARefreshedDelivery(t *testing.T) {
	dsn := os.Getenv("GAMEDATA_HANDLER_TEST_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_HANDLER_TEST_PG to a disposable database")
	}
	ctx := context.Background()
	pool, err := gamedata.NewPool(ctx, gamedata.PoolConfig{URL: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "DELETE FROM "+catalog.QuoteIdent(catalog.Mysekai().Table)+" WHERE _id = $1", birthdayPartyTestGameUserID); err != nil && !isMissingTable(err) {
		t.Fatal(err)
	}
	if _, err := gamedata.EnsureSchema(ctx, pool, true, catalog.Suite(), catalog.Mysekai()); err != nil {
		t.Fatal(err)
	}
	service := gamedata.NewService(pool)
	p := newBirthdayPartyPipeline(t, service)
	harvestMaps := func() string {
		t.Helper()
		row, err := service.Mysekai().Fetch(ctx, birthdayPartyTestGameUserID, "jp", nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, found, err := row.RawValue("updatedResources.userMysekaiHarvestMaps")
		if err != nil || !found {
			t.Fatalf("no stored harvest maps: found=%v err=%v", found, err)
		}
		return string(raw)
	}

	if _, err := p.upload(t, birthdayPartyDelivery(true, true)); err != nil {
		t.Fatalf("refreshed delivery: %v", err)
	}
	stored := harvestMaps()
	if !bytes.Contains([]byte(stored), []byte(`"mysekaiSiteId":5`)) {
		t.Fatalf("stored harvest maps = %s", stored)
	}

	if _, err := p.upload(t, birthdayPartyDelivery(false, false)); err == nil {
		t.Fatal("a delivery that did not refresh was stored")
	} else if _, skipped := uploadSkipReason(err); !skipped {
		t.Fatalf("not refreshed: %v, want a skip", err)
	}
	if _, err := p.upload(t, birthdayPartyDelivery(true, false)); err == nil {
		t.Fatal("a refreshed delivery without harvest maps was accepted")
	} else if _, skipped := uploadSkipReason(err); skipped {
		t.Fatal("a refreshed delivery without harvest maps was skipped, want an error")
	}
	if got := harvestMaps(); got != stored {
		t.Fatalf("stored harvest maps changed from %s to %s", stored, got)
	}
}

func isMissingTable(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("does not exist"))
}

type skippedTestResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ErrorCode    string `json:"error_code"`
		RequestID    string `json:"request_id"`
		UploadResult string `json:"upload_result"`
		Reason       string `json:"reason"`
	} `json:"updatedData"`
}

func skippingUpload(_ context.Context, _ []byte, _ harukiUtils.SupportedDataUploadServer, _ harukiUtils.UploadDataType, _ *int64, _ *string, _ *api.HarukiToolboxRouterHelpers, _ Dependencies, _ harukiUtils.UploadMethod, _ ...*platform.Attempt) (*harukiUtils.HandleDataResult, error) {
	return nil, &uploadSkippedError{reason: platform.SkipReasonBirthdayPartyNotRefreshed}
}

func doSkipRequest(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, skippedTestResponse) {
	t.Helper()
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var body skippedTestResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return resp, body
}

// checkSkippedResponse is the contract every JSON upload endpoint answers a
// skip with: 200 (no failure for clients that predate it), the two headers,
// and upload_result/reason in updatedData.
func checkSkippedResponse(t *testing.T, resp *http.Response, body skippedTestResponse) {
	t.Helper()
	if resp.StatusCode != fiber.StatusOK || body.Status != fiber.StatusOK {
		t.Fatalf("status = %d/%d, want 200", resp.StatusCode, body.Status)
	}
	if got := resp.Header.Get(proxyUploadResultHeader); got != "skipped" {
		t.Errorf("%s = %q, want skipped", proxyUploadResultHeader, got)
	}
	if got := resp.Header.Get(proxyUploadReasonHeader); got != "birthday_party_not_refreshed" {
		t.Errorf("%s = %q, want birthday_party_not_refreshed", proxyUploadReasonHeader, got)
	}
	if resp.Header.Get(proxyUploadErrorHeader) != "" || body.Data.ErrorCode != "" {
		t.Errorf("a skip carries an error code")
	}
	if body.Data.UploadResult != "skipped" || body.Data.Reason != "birthday_party_not_refreshed" {
		t.Errorf("updatedData = %+v, want upload_result skipped, reason birthday_party_not_refreshed", body.Data)
	}
	if body.Message != "Upload skipped: the birthday party delivery did not refresh the harvest points" {
		t.Errorf("message = %q", body.Message)
	}
}

func TestHarukiProxyV3AnswersASkippedUploadWith200(t *testing.T) {
	stubRunUpload(t, skippingUpload)
	deps := testUploadDependencies()
	app := fiber.New()
	app.Post("/:server/:user_id/:data_type/upload",
		func(c fiber.Ctx) error { c.Locals("userID", "actor"); return c.Next() },
		handleHarukiProxyUploadVersion(&api.HarukiToolboxRouterHelpers{}, deps, true))
	resp, body := doSkipRequest(t, app, httptest.NewRequest(http.MethodPost, "/jp/123/mysekai_birthday_party/upload", bytes.NewReader([]byte("payload"))))
	checkSkippedResponse(t, resp, body)
	if body.Data.RequestID == "" || body.Data.RequestID != resp.Header.Get("X-Request-ID") {
		t.Errorf("request id %q, header %q", body.Data.RequestID, resp.Header.Get("X-Request-ID"))
	}
}

func TestManualUploadAnswersASkippedBirthdayPartyDelivery(t *testing.T) {
	p := newBirthdayPartyPipeline(t, nil)
	app := fiber.New()
	app.Post("/:server/:user_id/:data_type/upload",
		func(c fiber.Ctx) error { c.Locals("userID", "owner"); return c.Next() },
		handleManualUpload(p.helper, p.deps))
	path := "/jp/" + strconv.FormatInt(birthdayPartyTestGameUserID, 10) + "/mysekai_birthday_party/upload"

	resp, body := doSkipRequest(t, app, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(p.pack(t, birthdayPartyDelivery(false, false)))))
	checkSkippedResponse(t, resp, body)

	resp, body = doSkipRequest(t, app, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(p.pack(t, birthdayPartyDelivery(true, false)))))
	if resp.StatusCode != fiber.StatusBadRequest || resp.Header.Get(proxyUploadResultHeader) != "" {
		t.Fatalf("refreshed without harvest maps: %d %+v", resp.StatusCode, body)
	}
}

func TestOAuth2UploadAnswersASkippedUploadWith200(t *testing.T) {
	stubRunUpload(t, skippingUpload)
	p := newBirthdayPartyPipeline(t, nil)
	app := fiber.New()
	app.Post("/:server/:data_type/:user_id",
		func(c fiber.Ctx) error { c.Locals("userID", "owner"); return c.Next() },
		handleOAuth2Upload(p.helper, p.deps))
	path := "/jp/mysekai_birthday_party/" + strconv.FormatInt(birthdayPartyTestGameUserID, 10)
	resp, body := doSkipRequest(t, app, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte("payload"))))
	checkSkippedResponse(t, resp, body)
}

func TestIOSScriptUploadAnswersASkippedUploadWith200(t *testing.T) {
	previous := config.Cfg.SekaiClient
	t.Cleanup(func() { config.Cfg.SekaiClient = previous })
	config.Cfg.SekaiClient = config.SekaiClientConfig{JPServerAPIHost: "jp.example.test", ENServerAPIHost: "en.example.test", TWServerAPIHost: "tw.example.test", KRServerAPIHost: "kr.example.test", CNServerAPIHost: "cn.example.test"}
	stubRunUpload(t, skippingUpload)

	db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "script-skip"))
	defer db.Close()
	ctx := context.Background()
	if _, err := db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.com").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.IOSScriptCode.Create().SetUserID("owner").SetUploadCode("owner-code").Save(ctx); err != nil {
		t.Fatal(err)
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: newTestAPIHelperWithRedis(t).DBManager.Redis}}
	d := testUploadDependencies()
	d.BackgroundTasks = background.InlineRunner{}
	app := fiber.New()
	app.Post("/:upload_code", handleIOSScriptUploadWithValidation(helper, d, d.DataHandlerLogger))
	req := httptest.NewRequest(http.MethodPost, "/owner-code", bytes.NewReader([]byte("payload")))
	req.Header.Set("X-Chunk-Index", "0")
	req.Header.Set("X-Total-Chunks", "1")
	req.Header.Set("X-Upload-Id", "skip")
	req.Header.Set("X-Original-Url", "https://jp.example.test/api/user/123/mysekai/birthday-party/4/delivery")
	resp, body := doSkipRequest(t, app, req)
	checkSkippedResponse(t, resp, body)
}

// ★ The iOS proxy path takes the same decision before the upload pipeline, so
// the game still gets its own response; the headers now also say why.
func TestIOSProxyBirthdayPartySkipCarriesTheReason(t *testing.T) {
	d := testUploadDependencies()
	pack := func(content map[string]any) []byte {
		raw, err := d.ServerCryptor.Pack(content, harukiUtils.SupportedDataUploadServerJP)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	previousCall := proxyCallAPI
	t.Cleanup(func() { proxyCallAPI = previousCall })
	var gameBody []byte
	proxyCallAPI = func(_ context.Context, _ map[string]string, _ string, _ harukiUtils.SupportedDataUploadServer, _ harukiUtils.UploadDataType, _ []byte, _ map[string]string, _ string, _ int64, _ *int64) (*harukiUtils.SekaiDataRetrieverResponse, error) {
		return &harukiUtils.SekaiDataRetrieverResponse{RawBody: gameBody, StatusCode: 200, NewHeaders: map[string][]string{"X-Game": {"1"}}}, nil
	}
	partyID := int64(4)
	app := fiber.New()
	app.Put("/ios/proxy/:server/user/:user_id/mysekai/birthday-party/:party_id/delivery", HandleProxyUpload("", harukiUtils.UploadDataTypeMysekaiBirthdayParty, &api.HarukiToolboxRouterHelpers{}, d, &partyID))

	for _, tc := range []struct {
		name               string
		delivery           map[string]any
		wantResult, reason string
	}{
		{"not refreshed", birthdayPartyDelivery(false, false), "skipped", "birthday_party_not_refreshed"},
		{"refreshed", birthdayPartyDelivery(true, true), "ok", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploaded := false
			stubRunUpload(t, func(ctx context.Context, data []byte, server harukiUtils.SupportedDataUploadServer, dataType harukiUtils.UploadDataType, gameUserID *int64, userID *string, h *api.HarukiToolboxRouterHelpers, deps Dependencies, m harukiUtils.UploadMethod, a ...*platform.Attempt) (*harukiUtils.HandleDataResult, error) {
				uploaded = true
				return succeedingUpload(ctx, data, server, dataType, gameUserID, userID, h, deps, m, a...)
			})
			gameBody = pack(tc.delivery)
			resp, err := app.Test(httptest.NewRequest(http.MethodPut, "/ios/proxy/jp/user/123/mysekai/birthday-party/4/delivery", nil))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 || !bytes.Equal(raw, gameBody) || resp.Header.Get("X-Game") != "1" {
				t.Fatalf("the game's response was not passed through: %d", resp.StatusCode)
			}
			if got := resp.Header.Get(proxyUploadResultHeader); got != tc.wantResult {
				t.Errorf("%s = %q, want %q", proxyUploadResultHeader, got, tc.wantResult)
			}
			if got := resp.Header.Get(proxyUploadReasonHeader); got != tc.reason {
				t.Errorf("%s = %q, want %q", proxyUploadReasonHeader, got, tc.reason)
			}
			if uploaded != (tc.wantResult == "ok") {
				t.Errorf("upload pipeline ran = %v", uploaded)
			}
		})
	}
}
