package userauthorizesocial

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/authorizesocialplatforminfo"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
)

func newAuthorizeSocialTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "_"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() {
		_ = client.Close()
	})

	if _, err := client.User.Create().
		SetID("u1").
		SetName("tester").
		SetEmail("tester@example.com").
		Save(context.Background()); err != nil {
		t.Fatalf("create user returned error: %v", err)
	}

	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			DB: client,
		},
	}
}

func TestSocialGrantListBoundsLegacyRowsAndPreservesDeletion(t *testing.T) {
	helper := newAuthorizeSocialTestHelper(t)
	db := helper.DBManager.DB
	for slot, comment := range map[int]string{1: "normal", 2: strings.Repeat("字", 400), maxUserSocialGrants + 1: "legacy slot"} {
		if err := db.AuthorizeSocialPlatformInfo.Create().SetUserID("u1").SetPlatform("qq").SetPlatformUserID(fmt.Sprint(slot)).
			SetPlatformID(slot).SetComment(comment).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	app := fiber.New()
	app.Get("/:toolbox_user_id", func(c fiber.Ctx) error {
		data, err := buildAuthorizedSocialPlatformUserData(c, db.AuthorizeSocialPlatformInfo, c.Params("toolbox_user_id"))
		if err != nil {
			return err
		}
		return c.JSON(data)
	})
	app.Delete("/:toolbox_user_id/:id", handleDeleteAuthorizeSocialPlatform(helper))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/u1", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "normal") || strings.Contains(string(body), "legacy slot") || strings.Contains(string(body), "字") {
		t.Fatalf("list returned %d", resp.StatusCode)
	}
	resp, err = app.Test(httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/u1/%d", maxUserSocialGrants+1), nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy deletion returned %d", resp.StatusCode)
	}
	if rows, err := db.AuthorizeSocialPlatformInfo.Query().Count(t.Context()); err != nil || rows != 2 {
		t.Fatalf("remaining grants = %d, %v", rows, err)
	}
}

func TestHandleCreateAuthorizeSocialPlatformAtIDCreatesWhenSlotDoesNotExist(t *testing.T) {
	t.Parallel()

	helper := newAuthorizeSocialTestHelper(t)
	app := fiber.New()
	app.Post("/:toolbox_user_id/:id", handleCreateAuthorizeSocialPlatformAtID(helper))

	req := httptest.NewRequest(http.MethodPost, "/u1/2", strings.NewReader(`{"platform":"qq","userId":"123123213","comment":"3213213","allowFastVerification":false}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	row, err := helper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
		Where(
			authorizesocialplatforminfo.UserIDEQ("u1"),
			authorizesocialplatforminfo.PlatformIDEQ(2),
		).
		Only(context.Background())
	if err != nil {
		t.Fatalf("query created row returned error: %v", err)
	}
	if row.Platform != "qq" {
		t.Fatalf("platform = %q, want %q", row.Platform, "qq")
	}
	if row.PlatformUserID != "123123213" {
		t.Fatalf("platform_user_id = %q, want %q", row.PlatformUserID, "123123213")
	}
	if row.Comment != "3213213" {
		t.Fatalf("comment = %q, want %q", row.Comment, "3213213")
	}
}

func TestHandleCreateAuthorizeSocialPlatformAtIDReturnsConflictWhenSlotExists(t *testing.T) {
	t.Parallel()

	helper := newAuthorizeSocialTestHelper(t)
	if _, err := helper.DBManager.DB.AuthorizeSocialPlatformInfo.Create().
		SetUserID("u1").
		SetPlatform("qq").
		SetPlatformUserID("old-user").
		SetPlatformID(2).
		SetComment("old-comment").
		SetAllowFastVerification(false).
		Save(context.Background()); err != nil {
		t.Fatalf("seed authorized social platform returned error: %v", err)
	}

	app := fiber.New()
	app.Post("/:toolbox_user_id/:id", handleCreateAuthorizeSocialPlatformAtID(helper))

	req := httptest.NewRequest(http.MethodPost, "/u1/2", strings.NewReader(`{"platform":"discord","userId":"new-user","comment":"new-comment","allowFastVerification":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusConflict)
	}
}

func TestHandleUpdateAuthorizeSocialPlatformUpdatesExistingSlot(t *testing.T) {
	t.Parallel()

	helper := newAuthorizeSocialTestHelper(t)
	if _, err := helper.DBManager.DB.AuthorizeSocialPlatformInfo.Create().
		SetUserID("u1").
		SetPlatform("qq").
		SetPlatformUserID("old-user").
		SetPlatformID(2).
		SetComment("old-comment").
		SetAllowFastVerification(false).
		Save(context.Background()); err != nil {
		t.Fatalf("seed authorized social platform returned error: %v", err)
	}

	app := fiber.New()
	app.Put("/:toolbox_user_id/:id", handleUpdateAuthorizeSocialPlatform(helper))

	req := httptest.NewRequest(http.MethodPut, "/u1/2", strings.NewReader(`{"platform":"discord","userId":"new-user","comment":"new-comment","allowFastVerification":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	row, err := helper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
		Where(
			authorizesocialplatforminfo.UserIDEQ("u1"),
			authorizesocialplatforminfo.PlatformIDEQ(2),
		).
		Only(context.Background())
	if err != nil {
		t.Fatalf("query updated row returned error: %v", err)
	}
	if row.Platform != "discord" {
		t.Fatalf("platform = %q, want %q", row.Platform, "discord")
	}
	if row.PlatformUserID != "new-user" {
		t.Fatalf("platform_user_id = %q, want %q", row.PlatformUserID, "new-user")
	}
	if row.Comment != "new-comment" {
		t.Fatalf("comment = %q, want %q", row.Comment, "new-comment")
	}
	if !row.AllowFastVerification {
		t.Fatalf("allow_fast_verification = false, want true")
	}
}

func TestHandleUpdateAuthorizeSocialPlatformReturnsNotFoundWhenSlotDoesNotExist(t *testing.T) {
	t.Parallel()

	helper := newAuthorizeSocialTestHelper(t)
	app := fiber.New()
	app.Put("/:toolbox_user_id/:id", handleUpdateAuthorizeSocialPlatform(helper))

	req := httptest.NewRequest(http.MethodPut, "/u1/2", strings.NewReader(`{"platform":"qq","userId":"123123213","comment":"3213213","allowFastVerification":false}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
}

func TestAutomaticSocialGrantAllocationReusesFreeSlot(t *testing.T) {
	helper := newAuthorizeSocialTestHelper(t)
	grants := helper.DBManager.DB.AuthorizeSocialPlatformInfo
	for slot := 1; slot <= maxUserSocialGrants; slot++ {
		if slot != 2 {
			grants.Create().SetUserID("u1").SetPlatform("qq").SetPlatformUserID(fmt.Sprint(slot)).
				SetPlatformID(slot).ExecX(t.Context())
		}
	}
	app := fiber.New()
	app.Post("/:toolbox_user_id", handleCreateAuthorizeSocialPlatform(helper))
	app.Post("/:toolbox_user_id/:id", handleCreateAuthorizeSocialPlatformAtID(helper))
	post := func(path, body, encoding string, want int, message string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", encoding)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want || !strings.Contains(string(raw), message) {
			t.Fatalf("grant at %s: status %d, body %s, err %v", path, resp.StatusCode, raw, err)
		}
	}
	body := `{"platform":"qq","userId":"fixture-account"}`
	post("/u1", body, "gzip", http.StatusBadRequest, "compressed authorization")
	post("/u1", strings.Repeat(" ", (8<<10)+1), "identity", http.StatusBadRequest, "body is too large")
	post("/u1", `{"platform":"qq","userId":"a","comment":"`+strings.Repeat("字", 400)+`"}`, "", http.StatusBadRequest, "invalid authorization field")
	post(fmt.Sprintf("/u1/%d", maxUserSocialGrants+1), body, "", http.StatusBadRequest, "exceeds the social authorization limit")
	post("/u1", body, "", http.StatusOK, "social platform created")
	if got := grants.Query().Where(authorizesocialplatforminfo.PlatformIDEQ(2)).OnlyX(t.Context()); got.PlatformUserID != "fixture-account" {
		t.Fatal("automatic allocation did not reuse slot 2")
	}
	post("/u1", body, "", http.StatusBadRequest, "authorization limit reached")
}
