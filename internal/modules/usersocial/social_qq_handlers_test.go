package usersocial

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

type successfulQQCaptcha struct{}

func (successfulQQCaptcha) Verify(context.Context, string, string) (*cloudflare.TurnstileResponse, error) {
	return &cloudflare.TurnstileResponse{Success: true}, nil
}

func TestQQLinkageRequiresAnOwnedChallengeAndIsHiddenBeforeVerification(t *testing.T) {
	helper := newSocialRateLimitTestHelper(t)
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	helper.DBManager.DB = db
	ctx := t.Context()
	for _, id := range []string{"owner", "other"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").ExecX(ctx)
	}
	db.SocialPlatformInfo.Create().SetUserID("other").SetPlatform("qq").SetPlatformUserID("123456").SetVerified(true).ExecX(ctx)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", c.Get("Test-Actor"))
		return c.Next()
	})
	app.Post("/send", handleSendQQMail(helper, successfulQQCaptcha{}))
	app.Post("/verify", handleVerifyQQMail(helper))
	request := func(path, actor, payload string, want int) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-Actor", actor)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s returned %d, want %d", path, resp.StatusCode, want)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	// The mail transport is intentionally absent. Both inputs reach the same
	// send boundary without leaking whether the target is already linked.
	linked := request("/send", "owner", `{"qq":"123456"}`, http.StatusServiceUnavailable)
	unlinked := request("/send", "owner", `{"qq":"654321"}`, http.StatusServiceUnavailable)
	if linked != unlinked {
		t.Fatal("initial send response disclosed whether the account was linked")
	}
	request("/send", "owner", `{"qq":"invalid"}`, http.StatusBadRequest)
	request("/send", "owner", `{"qq":""}`, http.StatusBadRequest)
	request("/verify", "owner", `{"qq":"invalid","oneTimePassword":"012345"}`, http.StatusBadRequest)

	cache := helper.DBManager.Redis
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey("owner", "654321@qq.com")
	if err := cache.Redis.Set(ctx, key, "fixture:012345", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	request("/verify", "other", `{"qq":"654321","oneTimePassword":"012345"}`, http.StatusBadRequest)
	request("/verify", "owner", `{"qq":"654321","oneTimePassword":"012345"}`, http.StatusOK)
	id := db.SocialPlatformInfo.Query().Where(socialplatforminfo.PlatformUserIDEQ("654321")).OnlyX(ctx).UserSocialPlatformInfo
	if id != "owner" {
		t.Fatalf("verified link belongs to %q", id)
	}
}

func TestClearSocialLinkRevokesLegacyGrantsForOwnerOnly(t *testing.T) {
	helper := newSocialRateLimitTestHelper(t)
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	helper.DBManager.DB = db
	ctx := t.Context()
	for _, id := range []string{"owner", "other"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").ExecX(ctx)
		db.AuthorizeSocialPlatformInfo.Create().SetUserID(id).SetPlatform("qq").SetPlatformUserID("123456").SetPlatformID(1).ExecX(ctx)
	}
	app := fiber.New()
	app.Delete("/", func(c fiber.Ctx) error {
		c.Locals("userID", "owner")
		return c.Next()
	}, handleClearSocialPlatform(helper))
	for _, want := range []int{http.StatusOK, http.StatusBadRequest} {
		resp, err := app.Test(httptest.NewRequest(http.MethodDelete, "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("clear returned %d, want %d", resp.StatusCode, want)
		}
	}
	if grant := db.AuthorizeSocialPlatformInfo.Query().OnlyX(ctx); grant.UserID != "other" {
		t.Fatalf("remaining grant belongs to %q", grant.UserID)
	}
}
