package usergamebindings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	harukiSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"

	"entgo.io/ent"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func newDefaultBindingTestHelper(t *testing.T, dbName string) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *postgresql.Client, context.Context) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+dbName+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{DB: client},
	}
	return helper, client, context.Background()
}

func TestDefaultBindingUpdateRollsBackWhenTargetIsNoLongerVerified(t *testing.T) {
	helper, db, ctx := newDefaultBindingTestHelper(t, "binding-default-guard")
	createDefaultBindingTestUser(t, db, ctx, "owner")
	createBindingViaSave(t, helper, ctx, "jp", "111", "owner")
	createBindingViaSave(t, helper, ctx, "jp", "222", "owner")
	targetID := db.GameAccountBinding.Query().Where(gameaccountbinding.GameUserIDEQ("222")).OnlyIDX(ctx)
	db.GameAccountBinding.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			binding := mutation.(*postgresql.GameAccountBindingMutation)
			if value, ok := binding.IsDefault(); binding.Op().Is(ent.OpUpdateOne) && ok && value {
				if err := binding.Client().GameAccountBinding.Update().Where(gameaccountbinding.IDEQ(targetID)).SetVerified(false).Exec(ctx); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
	app := fiber.New()
	app.Put("/:server/:game_user_id", func(c fiber.Ctx) error {
		c.Locals("userID", "owner")
		return c.Next()
	}, handleUpdateGameAccountBinding(helper))
	req := httptest.NewRequest(http.MethodPut, "/jp/222", strings.NewReader(`{"suite":{},"mysekai":{},"isDefault":true}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
	if !queryBindingIsDefault(t, db, ctx, "jp", "111") || queryBindingIsDefault(t, db, ctx, "jp", "222") {
		t.Fatal("the rejected update changed the user's default binding")
	}
}

func createDefaultBindingTestUser(t *testing.T, client *postgresql.Client, ctx context.Context, id string) {
	t.Helper()
	if _, err := client.User.Create().
		SetID(id).
		SetName(id).
		SetEmail(id + "@example.com").
		Save(ctx); err != nil {
		t.Fatalf("create user %s returned error: %v", id, err)
	}
}

func createBindingViaSave(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, ctx context.Context, server, gameUserID, owner string) {
	t.Helper()
	if _, err := saveGameAccountBinding(ctx, helper, nil, server, gameUserID, owner, harukiAPIHelper.CreateGameAccountBindingPayload{
		Suite:   &harukiSchema.SuiteDataPrivacySettings{},
		MySekai: &harukiSchema.MysekaiDataPrivacySettings{},
	}); err != nil {
		t.Fatalf("saveGameAccountBinding(%s/%s) returned error: %v", server, gameUserID, err)
	}
}

func queryBindingIsDefault(t *testing.T, client *postgresql.Client, ctx context.Context, server, gameUserID string) bool {
	t.Helper()
	binding, err := client.GameAccountBinding.Query().
		Where(
			gameaccountbinding.ServerEQ(server),
			gameaccountbinding.GameUserIDEQ(gameUserID),
		).
		Only(ctx)
	if err != nil {
		t.Fatalf("query binding %s/%s returned error: %v", server, gameUserID, err)
	}
	return binding.IsDefault
}

func TestFirstBindingBecomesDefaultAndLaterOnesDoNot(t *testing.T) {
	helper, client, ctx := newDefaultBindingTestHelper(t, "binding-default-first")
	createDefaultBindingTestUser(t, client, ctx, "owner")

	createBindingViaSave(t, helper, ctx, "jp", "111", "owner")
	createBindingViaSave(t, helper, ctx, "cn", "222", "owner")

	if !queryBindingIsDefault(t, client, ctx, "jp", "111") {
		t.Fatalf("first binding should become the default")
	}
	if queryBindingIsDefault(t, client, ctx, "cn", "222") {
		t.Fatalf("second binding must not steal the default")
	}
}

func TestTransferredBindingDropsPreviousOwnersDefaultFlag(t *testing.T) {
	helper, client, ctx := newDefaultBindingTestHelper(t, "binding-default-transfer")
	createDefaultBindingTestUser(t, client, ctx, "old-owner")
	createDefaultBindingTestUser(t, client, ctx, "new-owner")

	// old-owner's first (and default) binding; new-owner already has a default.
	createBindingViaSave(t, helper, ctx, "jp", "111", "old-owner")
	createBindingViaSave(t, helper, ctx, "jp", "999", "new-owner")

	existing, err := client.GameAccountBinding.Query().
		Where(gameaccountbinding.GameUserIDEQ("111")).
		WithUser().
		Only(ctx)
	if err != nil {
		t.Fatalf("query existing binding returned error: %v", err)
	}
	if _, err := saveGameAccountBinding(ctx, helper, existing, "jp", "111", "new-owner", harukiAPIHelper.CreateGameAccountBindingPayload{
		Suite:   &harukiSchema.SuiteDataPrivacySettings{},
		MySekai: &harukiSchema.MysekaiDataPrivacySettings{},
	}); err != nil {
		t.Fatalf("transfer saveGameAccountBinding returned error: %v", err)
	}

	if queryBindingIsDefault(t, client, ctx, "jp", "111") {
		t.Fatalf("transferred binding must not keep the previous owner's default flag")
	}
	if !queryBindingIsDefault(t, client, ctx, "jp", "999") {
		t.Fatalf("new owner's existing default must survive the transfer")
	}
}

func TestTransferredBindingBecomesDefaultWhenNewOwnerHasNone(t *testing.T) {
	helper, client, ctx := newDefaultBindingTestHelper(t, "binding-default-transfer-empty")
	createDefaultBindingTestUser(t, client, ctx, "old-owner")
	createDefaultBindingTestUser(t, client, ctx, "new-owner")

	createBindingViaSave(t, helper, ctx, "jp", "111", "old-owner")

	existing, err := client.GameAccountBinding.Query().
		Where(gameaccountbinding.GameUserIDEQ("111")).
		WithUser().
		Only(ctx)
	if err != nil {
		t.Fatalf("query existing binding returned error: %v", err)
	}
	if _, err := saveGameAccountBinding(ctx, helper, existing, "jp", "111", "new-owner", harukiAPIHelper.CreateGameAccountBindingPayload{
		Suite:   &harukiSchema.SuiteDataPrivacySettings{},
		MySekai: &harukiSchema.MysekaiDataPrivacySettings{},
	}); err != nil {
		t.Fatalf("transfer saveGameAccountBinding returned error: %v", err)
	}

	if !queryBindingIsDefault(t, client, ctx, "jp", "111") {
		t.Fatalf("binding transferred to an owner without a default should become their default")
	}
}
