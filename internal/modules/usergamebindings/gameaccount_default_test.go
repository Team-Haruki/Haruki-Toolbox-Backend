package usergamebindings

import (
	"context"
	"testing"

	harukiSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"

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
