package adminusers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/gofiber/fiber/v3"
)

func TestManagedBindingWritesRecheckOwnershipAndInvalidateGrants(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	db := helper.DBManager.DB
	ctx := t.Context()
	for _, id := range []string{"owner", "other"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").ExecX(ctx)
	}
	injected := ent.Op(0)
	db.GameAccountBinding.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if mutation.Op().Is(injected) {
				return nil, errors.New("fixture write failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "actor")
		c.Locals("userRole", roleAdmin)
		return c.Next()
	})
	app.Put("/:target_user_id/:server/:game_user_id", handleUpsertUserGameAccountBinding(helper))
	app.Delete("/:target_user_id/:server/:game_user_id", handleDeleteUserGameAccountBinding(helper))
	request := func(method, path string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"suite":{},"mysekai":{}}`))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d", method, path, response.StatusCode, want)
		}
	}
	injected = ent.OpCreate
	request(http.MethodPut, "/owner/jp/123", http.StatusInternalServerError)
	injected = 0
	request(http.MethodPut, "/owner/jp/000123", http.StatusOK)
	if row := db.GameAccountBinding.Query().OnlyX(ctx); !row.Verified || row.GameUserID != "123" {
		t.Fatalf("binding was not normalized and verified: %+v", row)
	}
	request(http.MethodPut, "/other/jp/123", http.StatusConflict)
	request(http.MethodDelete, "/other/jp/123", http.StatusConflict)
	request(http.MethodDelete, "/owner/jp/999", http.StatusNotFound)

	if _, err := db.UpsertGameAccountDataGrant(ctx, "owner", "other", "jp", "123", "suite", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.GameAccountBinding.Update().Where(gameaccountbinding.GameUserIDEQ("123")).SetVerified(false).ExecX(ctx)
	injected = ent.OpUpdateOne
	request(http.MethodPut, "/owner/jp/123", http.StatusInternalServerError)
	if db.GameAccountDataGrant.Query().CountX(ctx) != 1 {
		t.Fatal("failed binding update invalidated the grant")
	}
	injected = 0
	request(http.MethodPut, "/owner/jp/123", http.StatusOK)
	if db.GameAccountDataGrant.Query().CountX(ctx) != 0 {
		t.Fatal("reverification preserved an old grant")
	}
	injected = ent.OpDeleteOne
	request(http.MethodDelete, "/owner/jp/123", http.StatusInternalServerError)
	injected = 0
	request(http.MethodDelete, "/owner/jp/123", http.StatusOK)
	if db.GameAccountBinding.Query().CountX(ctx) != 0 {
		t.Fatal("delete returned success without removing the binding")
	}
}

func TestManagedBindingWritesRejectPromotionWhileWaiting(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			helper := newAdminUserTicketNotificationTestHelper(t)
			db := helper.DBManager.DB
			ctx := t.Context()
			db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.test").ExecX(ctx)
			db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("42").SetUserID("owner").ExecX(ctx)
			release, err := db.LockGameAccountMutation(ctx, "jp", "42")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			seen := make(chan struct{}, 1)
			db.User.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					value, err := next.Query(ctx, q)
					select {
					case seen <- struct{}{}:
					default:
					}
					return value, err
				})
			}))
			app := fiber.New()
			app.Use(func(c fiber.Ctx) error {
				c.Locals("userID", "actor")
				c.Locals("userRole", roleAdmin)
				return c.Next()
			})
			app.Put("/:target_user_id/:server/:game_user_id", handleUpsertUserGameAccountBinding(helper))
			app.Delete("/:target_user_id/:server/:game_user_id", handleDeleteUserGameAccountBinding(helper))
			result := make(chan int, 1)
			go func() {
				req := httptest.NewRequest(method, "/owner/jp/42", strings.NewReader(`{}`))
				req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
				response, err := app.Test(req)
				if err != nil {
					result <- 0
					return
				}
				_ = response.Body.Close()
				result <- response.StatusCode
			}()
			select {
			case <-seen:
			case <-time.After(time.Second):
				t.Fatal("request did not reach the initial owner check")
			}
			db.User.UpdateOneID("owner").SetRole(user.RoleSuperAdmin).ExecX(ctx)
			release()
			if actual := <-result; (actual != http.StatusForbidden && actual != http.StatusNotFound) || db.GameAccountBinding.Query().OnlyX(ctx).Verified {
				t.Fatalf("delayed binding mutation after promotion returned %d", actual)
			}
		})
	}
}
