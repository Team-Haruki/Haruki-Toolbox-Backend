package admingamebindings

import (
	"context"
	"errors"
	"io"
	"time"

	"entgo.io/ent"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestParseAdminGlobalGameBindingSort(t *testing.T) {
	sortValue, err := parseAdminGlobalGameBindingSort("")
	if err != nil {
		t.Fatalf("parseAdminGlobalGameBindingSort returned error: %v", err)
	}
	if sortValue != defaultAdminGlobalGameBindingSort {
		t.Fatalf("sortValue = %q, want %q", sortValue, defaultAdminGlobalGameBindingSort)
	}

	if _, err := parseAdminGlobalGameBindingSort("bad_sort"); err == nil {
		t.Fatalf("expected invalid sort option to fail")
	}
}

func TestParseAdminGlobalGameBindingQueryFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminGlobalGameBindingQueryFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?q=124&server=jp&game_user_id=1241241241&user_id=8532047909&verified=true&page=2&page_size=20&sort=user_id_asc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid server", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?server=us", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid game user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?game_user_id=abc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminGameBindingReassignPayload(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		targetUserID, err := parseAdminGameBindingReassignPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(targetUserID)
	})

	t.Run("camel case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"targetUserId":"1241241241"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "1241241241" {
			t.Fatalf("response body = %q, want %q", string(body), "1241241241")
		}
	})

	t.Run("snake case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"target_user_id":"8532047909"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "8532047909" {
			t.Fatalf("response body = %q, want %q", string(body), "8532047909")
		}
	})

	t.Run("missing target user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestSanitizeAdminBatchGameBindingRefs(t *testing.T) {
	items, err := sanitizeAdminBatchGameBindingRefs([]adminGlobalGameBindingRef{
		{Server: "jp", GameUserID: "123"},
		{Server: "jp", GameUserID: "123"},
		{Server: "en", GameUserIDSnake: "456"},
	})
	if err != nil {
		t.Fatalf("sanitizeAdminBatchGameBindingRefs returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	if _, err := sanitizeAdminBatchGameBindingRefs([]adminGlobalGameBindingRef{
		{Server: "jp", GameUserID: "abc"},
	}); err == nil {
		t.Fatalf("expected invalid game user id to fail")
	}
}

func TestSanitizeAdminBatchGameBindingReassignItems(t *testing.T) {
	items, err := sanitizeAdminBatchGameBindingReassignItems([]adminGlobalGameBindingBatchReassignItem{
		{Server: "jp", GameUserID: "123", TargetUserID: "u1"},
		{Server: "jp", GameUserID: "123", TargetUserID: "u1"},
		{Server: "en", GameUserIDSnake: "456", TargetUserIDSnake: "u2"},
	})
	if err != nil {
		t.Fatalf("sanitizeAdminBatchGameBindingReassignItems returned error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}

	if _, err := sanitizeAdminBatchGameBindingReassignItems([]adminGlobalGameBindingBatchReassignItem{
		{Server: "jp", GameUserID: "123", TargetUserID: "u1"},
		{Server: "jp", GameUserID: "123", TargetUserID: "u2"},
	}); err == nil {
		t.Fatalf("expected duplicate binding with conflicting target to fail")
	}
}

func TestAdminGlobalGameBindingRoutePermissions(t *testing.T) {
	app := buildRoleProtectedApp(
		http.MethodPut,
		"/api/admin/game-account-bindings/:server/:game_user_id/reassign",
		[]string{roleAdmin, roleSuperAdmin},
		func(c fiber.Ctx) error {
			return c.SendString(c.Params("server") + "," + c.Params("game_user_id"))
		},
	)

	t.Run("missing session", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/game-account-bindings/jp/12345/reassign", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("user role denied", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/game-account-bindings/jp/12345/reassign", nil)
		req.Header.Set("X-User-ID", "2002")
		req.Header.Set("X-Role", roleUser)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("admin role allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/game-account-bindings/jp/12345/reassign", nil)
		req.Header.Set("X-User-ID", "2002")
		req.Header.Set("X-Role", roleAdmin)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})
}

func TestManagedBindingReassignmentChecksBothCurrentRoles(t *testing.T) {
	for _, promoted := range []string{"owner", "destination"} {
		t.Run(promoted, func(t *testing.T) {
			ctx := t.Context()
			db := enttest.Open(t, "sqlite3", "file:managed-binding-"+promoted+"?mode=memory&cache=shared&_fk=1")
			t.Cleanup(func() { _ = db.Close() })
			for _, id := range []string{"owner", "destination"} {
				db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").SaveX(ctx)
			}
			binding := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").SaveX(ctx)
			stale := db.GameAccountBinding.Query().WithUser().OnlyX(ctx)
			db.User.UpdateOneID(promoted).SetRole(user.RoleSuperAdmin).SaveX(ctx)
			helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
			app := fiber.New()
			app.Put("/", func(c fiber.Ctx) error {
				return mutateManagedGameBinding(c, helper, stale, "actor", roleAdmin, "destination")
			})
			response, err := app.Test(httptest.NewRequest(http.MethodPut, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusForbidden || db.GameAccountBinding.GetX(ctx, binding.ID).QueryUser().OnlyIDX(ctx) != "owner" {
				t.Fatalf("reassignment with protected %s returned %d", promoted, response.StatusCode)
			}
		})
	}
}

func TestGlobalBindingActionsRevokeGrantsOnlyAfterSuccessfulCommit(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	ctx := t.Context()
	for _, id := range []string{"first", "second", "delegate"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").ExecX(ctx)
	}
	for _, id := range []string{"42", "43"} {
		db.GameAccountBinding.Create().SetServer("jp").SetGameUserID(id).SetVerified(true).SetUserID("first").ExecX(ctx)
		if _, err := db.UpsertGameAccountDataGrant(ctx, "first", "delegate", "jp", id, "suite", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	inject := false
	db.GameAccountBinding.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if inject && m.Op().Is(ent.OpDeleteOne|ent.OpUpdateOne) {
				return nil, errors.New("fixture binding database failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "actor")
		c.Locals("userRole", roleAdmin)
		return c.Next()
	})
	app.Delete("/single/:server/:game_user_id", handleAdminDeleteGlobalGameAccountBinding(helper))
	app.Put("/single/:server/:game_user_id", handleAdminReassignGlobalGameAccountBinding(helper))
	app.Post("/batch-delete", handleAdminBatchDeleteGlobalGameAccountBindings(helper))
	app.Post("/batch-reassign", handleAdminBatchReassignGlobalGameAccountBindings(helper))
	request := func(method, path, payload string, want int, message string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want || !strings.Contains(string(body), message) {
			t.Fatalf("%s %s returned %d: %s (%v)", method, path, resp.StatusCode, body, err)
		}
	}
	const reassign = `{"targetUserId":"second"}`
	const batchDelete = `{"items":[{"server":"jp","gameUserId":"43"}]}`
	const batchReassign = `{"items":[{"server":"jp","gameUserId":"42","targetUserId":"second"}]}`
	inject = true
	request(http.MethodPut, "/single/jp/42", reassign, http.StatusInternalServerError, "failed to reassign binding")
	request(http.MethodDelete, "/single/jp/43", "", http.StatusInternalServerError, "failed to delete binding")
	request(http.MethodPost, "/batch-delete", batchDelete, http.StatusOK, adminBatchResultCodeDeleteBindingFailed)
	request(http.MethodPost, "/batch-reassign", batchReassign, http.StatusOK, adminBatchResultCodeReassignBindingFailed)
	if db.GameAccountDataGrant.Query().CountX(ctx) != 2 {
		t.Fatal("failed operations revoked stored permissions")
	}
	inject = false
	request(http.MethodPost, "/batch-reassign", batchReassign, http.StatusOK, `"success":true`)
	request(http.MethodDelete, "/single/jp/43", "", http.StatusOK, "binding deleted")
	row := db.GameAccountBinding.Query().Where(gameaccountbinding.GameUserIDEQ("42")).OnlyX(ctx)
	if row.QueryUser().OnlyIDX(ctx) != "second" || db.GameAccountDataGrant.Query().CountX(ctx) != 0 {
		t.Fatal("committed transfer or deletion preserved the old authorization")
	}
}
