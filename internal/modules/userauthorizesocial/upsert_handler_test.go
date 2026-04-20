package userauthorizesocial

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"haruki-suite/utils/database"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"

	harukiAPIHelper "haruki-suite/utils/api"
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
