package adminusers

import (
	"context"
	json "encoding/json/v2"

	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"entgo.io/ent"
	"github.com/gofiber/fiber/v3"
)

func TestHandleListUsersScopesResultsBeforePagination(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	seedAdminUserTicketNotificationTestUser(t, helper, "actor-admin", "actor", "actor@example.com", roleAdmin, false, false)
	seedAdminUserTicketNotificationTestUser(t, helper, "ordinary-user", "ordinary", "ordinary@example.com", roleUser, false, false)
	seedAdminUserTicketNotificationTestUser(t, helper, "hidden-super", "super", "super@example.com", roleSuperAdmin, false, false)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "actor-admin")
		c.Locals("userRole", roleAdmin)
		return c.Next()
	})
	app.Get("/", handleListUsers(helper))

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("app.Test() error: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, fiber.StatusOK)
	}

	var payload harukiAPIHelper.GenericResponse[adminUserListResponse]
	if err := json.UnmarshalRead(response.Body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.UpdatedData == nil {
		t.Fatal("updatedData is nil")
	}
	if payload.UpdatedData.Total != 1 || len(payload.UpdatedData.Items) != 1 {
		t.Fatalf("scoped total/items = %d/%d, want 1/1", payload.UpdatedData.Total, len(payload.UpdatedData.Items))
	}
	if payload.UpdatedData.Items[0].UserID != "ordinary-user" {
		t.Fatalf("visible user = %q, want ordinary-user", payload.UpdatedData.Items[0].UserID)
	}
}

func TestBatchBanGuardMissSkipsAccessRevocation(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	db := helper.DBManager.DB
	seedAdminUserTicketNotificationTestUser(t, helper, "target", "target", "target@example.com", roleUser, false, false)
	db.User.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if banned, ok := mutation.(*postgresql.UserMutation).Banned(); mutation.Op().Is(ent.OpUpdate) && ok && banned {
				return 0, nil
			}
			return next.Mutate(ctx, mutation)
		})
	})
	var revocations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		revocations.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	hydra := oauth2.NewHydraConfig(oauth2.HydraConfigOptions{Provider: oauth2.ProviderHydra, AdminURL: server.URL})
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		c.Locals("userID", "actor")
		c.Locals("userRole", roleAdmin)
		return c.Next()
	}, handleBatchBanUsers(helper, hydra))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"userIds":["target"]}`))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload harukiAPIHelper.GenericResponse[batchUserOperationResponse]
	if err := json.UnmarshalRead(response.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || payload.UpdatedData == nil || payload.UpdatedData.Failed != 1 || payload.UpdatedData.Success != 0 {
		t.Fatalf("status = %d, batch = %+v", response.StatusCode, payload.UpdatedData)
	}
	if revocations.Load() != 0 || db.User.GetX(t.Context(), "target").Banned {
		t.Fatal("a missed ban mutation changed the target's access")
	}
}

func TestManagedUserMutationsWaitForTargetRole(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		handler    func(*harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler
	}{
		{"password", `{}`, handleResetUserPassword},
		{"email", `{"email":"updated@example.test"}`, handleUpdateUserEmail},
		{"ios-regenerate", `{}`, handleRegenerateUserIOSUploadCode},
		{"ios-clear", `{}`, handleClearUserIOSUploadCode},
		{"force-logout", `{}`, handleForceLogoutUser},
		{"revoke-oauth", `{"revokeAll":true}`, func(h *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
			return handleRevokeUserOAuth(h, nil)
		}},
		{"social-link", `{}`, handleUpsertUserSocialPlatform},
		{"social-unlink", `{}`, handleClearUserSocialPlatform},
		{"social-grant", `{}`, handleUpsertUserAuthorizedSocialPlatform},
		{"social-revoke", `{}`, handleDeleteUserAuthorizedSocialPlatform},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := newAdminUserTicketNotificationTestHelper(t)
			db := helper.DBManager.DB
			db.User.Create().SetID("target").SetName("Target").SetEmail("target@example.test").SetKratosIdentityID("identity-target").SaveX(t.Context())
			var providerCalls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				providerCalls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer provider.Close()
			helper.SessionHandler = harukiAPIHelper.NewSessionHandler(nil, "test-sign-key")
			helper.SessionHandler.KratosAdminURL = provider.URL

			entered := make(chan struct{})
			app := fiber.New()
			app.Post("/:target_user_id", func(c fiber.Ctx) error {
				c.Locals("userID", "actor")
				c.Locals("userRole", roleAdmin)
				close(entered)
				return c.Next()
			}, tc.handler(helper))
			release, err := db.LockUserMutations(t.Context(), "target")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			status := make(chan int, 1)
			go func() {
				req := httptest.NewRequest(http.MethodPost, "/target", strings.NewReader(tc.body))
				req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
				resp, err := app.Test(req)
				if err != nil {
					status <- 0
					return
				}
				_ = resp.Body.Close()
				status <- resp.StatusCode
			}()
			<-entered
			select {
			case code := <-status:
				t.Fatalf("credential action returned %d while the role reservation was held", code)
			case <-time.After(30 * time.Millisecond):
			}
			db.User.UpdateOneID("target").SetRole(user.RoleSuperAdmin).SaveX(t.Context())
			release()
			if code := <-status; code != http.StatusForbidden || providerCalls.Load() != 0 {
				t.Fatalf("after promotion: status=%d, provider calls=%d", code, providerCalls.Load())
			}
		})
	}
}

func requireFiberErrorCode(t *testing.T, err error, wantCode int) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	fiberErr, ok := err.(*fiber.Error)
	if !ok {
		t.Fatalf("error type = %T, want *fiber.Error", err)
	}
	if fiberErr.Code != wantCode {
		t.Fatalf("error code = %d, want %d", fiberErr.Code, wantCode)
	}
}

func TestParseAdminUsersSort(t *testing.T) {
	t.Run("default sort when empty", func(t *testing.T) {
		sortValue, err := parseAdminUsersSort("")
		if err != nil {
			t.Fatalf("parseAdminUsersSort returned error: %v", err)
		}
		if sortValue != defaultAdminUsersSort {
			t.Fatalf("sortValue = %q, want %q", sortValue, defaultAdminUsersSort)
		}
	})

	t.Run("accept valid sort", func(t *testing.T) {
		sortValue, err := parseAdminUsersSort(adminUsersSortNameAsc)
		if err != nil {
			t.Fatalf("parseAdminUsersSort returned error: %v", err)
		}
		if sortValue != adminUsersSortNameAsc {
			t.Fatalf("sortValue = %q, want %q", sortValue, adminUsersSortNameAsc)
		}
	})

	t.Run("accept created at sort", func(t *testing.T) {
		sortValue, err := parseAdminUsersSort(adminUsersSortCreatedAtDesc)
		if err != nil {
			t.Fatalf("parseAdminUsersSort returned error: %v", err)
		}
		if sortValue != adminUsersSortCreatedAtDesc {
			t.Fatalf("sortValue = %q, want %q", sortValue, adminUsersSortCreatedAtDesc)
		}
	})

	t.Run("reject invalid sort", func(t *testing.T) {
		if _, err := parseAdminUsersSort("random"); err == nil {
			t.Fatalf("expected invalid sort to fail")
		}
	})
}

func TestParseAdminUserQueryFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminUserQueryFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("accept valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?q=test&role=admin&banned=true&allow_cn_mysekai=true&created_from=2026-03-01T00:00:00Z&created_to=2026-03-08T00:00:00Z&page=2&page_size=20&sort=created_at_desc", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("reject invalid role", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?role=owner", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid banned filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?banned=not_bool", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid allow_cn_mysekai filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?allow_cn_mysekai=not_bool", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("accept camel case allow filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?allowCNMysekai=false", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("reject oversized page_size", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page_size=999", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject invalid created_from", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?created_from=not-a-time", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("reject reversed created range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?created_from=2026-03-09T00:00:00Z&created_to=2026-03-08T00:00:00Z", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestEnsureAdminCanManageTargetUser(t *testing.T) {
	t.Run("super admin can manage super admin", func(t *testing.T) {
		if err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleSuperAdmin, "2", roleSuperAdmin); err != nil {
			t.Fatalf("ensureAdminCanManageTargetUser returned error: %v", err)
		}
	})

	t.Run("admin cannot manage super admin", func(t *testing.T) {
		err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleAdmin, "2", roleSuperAdmin)
		requireFiberErrorCode(t, err, fiber.StatusForbidden)
	})

	t.Run("admin can manage normal admin", func(t *testing.T) {
		if err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleAdmin, "2", roleAdmin); err != nil {
			t.Fatalf("ensureAdminCanManageTargetUser returned error: %v", err)
		}
	})

	t.Run("cannot manage self", func(t *testing.T) {
		err := adminCoreModule.EnsureAdminCanManageTargetUser("1", roleSuperAdmin, "1", roleUser)
		requireFiberErrorCode(t, err, fiber.StatusBadRequest)
	})
}

func TestSanitizeBanReason(t *testing.T) {
	t.Run("nil reason", func(t *testing.T) {
		reason, err := sanitizeBanReason(nil)
		if err != nil {
			t.Fatalf("sanitizeBanReason returned error: %v", err)
		}
		if reason != nil {
			t.Fatalf("reason = %v, want nil", reason)
		}
	})

	t.Run("empty reason", func(t *testing.T) {
		raw := "   "
		reason, err := sanitizeBanReason(&raw)
		if err != nil {
			t.Fatalf("sanitizeBanReason returned error: %v", err)
		}
		if reason != nil {
			t.Fatalf("reason = %v, want nil", reason)
		}
	})

	t.Run("trim valid reason", func(t *testing.T) {
		raw := "  spam upload  "
		reason, err := sanitizeBanReason(&raw)
		if err != nil {
			t.Fatalf("sanitizeBanReason returned error: %v", err)
		}
		if reason == nil || *reason != "spam upload" {
			t.Fatalf("reason = %v, want \"spam upload\"", reason)
		}
	})

	t.Run("reject too long reason", func(t *testing.T) {
		raw := strings.Repeat("a", 501)
		_, err := sanitizeBanReason(&raw)
		requireFiberErrorCode(t, err, fiber.StatusBadRequest)
	})
}

func TestBuildAdminUserListItems(t *testing.T) {
	created := time.Date(2026, 3, 8, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	rows := []*postgresql.User{
		{
			ID:                              "1001",
			Name:                            "alice",
			Email:                           "alice@example.com",
			Role:                            "admin",
			Banned:                          false,
			AllowCnMysekai:                  true,
			TicketEmailNotificationsEnabled: true,
			CreatedAt:                       &created,
		},
		{
			ID:             "1002",
			Name:           "bob",
			Email:          "bob@example.com",
			Role:           "user",
			Banned:         true,
			AllowCnMysekai: false,
		},
	}

	items := buildAdminUserListItems(rows)
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].CreatedAt == nil {
		t.Fatalf("items[0].CreatedAt = nil, want non-nil")
	}
	if items[0].CreatedAt.Location() != time.UTC {
		t.Fatalf("items[0].CreatedAt location = %s, want UTC", items[0].CreatedAt.Location())
	}
	if got := items[0].CreatedAt.Format(time.RFC3339); got != "2026-03-08T01:30:00Z" {
		t.Fatalf("items[0].CreatedAt = %s, want 2026-03-08T01:30:00Z", got)
	}
	if items[1].CreatedAt != nil {
		t.Fatalf("items[1].CreatedAt = %v, want nil", items[1].CreatedAt)
	}
	if !items[0].AllowCNMysekai {
		t.Fatalf("items[0].AllowCNMysekai = false, want true")
	}
	if !items[0].TicketEmailNotificationsEnabled {
		t.Fatalf("items[0].TicketEmailNotificationsEnabled = false, want true")
	}
	if items[1].AllowCNMysekai {
		t.Fatalf("items[1].AllowCNMysekai = true, want false")
	}
}

func TestSingleAndBatchRoleChangesCommitOnVisibleAccounts(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	db := helper.DBManager.DB
	db.User.Create().SetID("target").SetName("Target").SetEmail("target@example.test").ExecX(t.Context())
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "actor")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Put("/role/:target_user_id", handleUpdateUserRole(helper))
	app.Post("/batch", handleBatchUpdateUserRole(helper))
	for _, tc := range []struct {
		method, path, body string
		role               user.Role
	}{
		{http.MethodPut, "/role/target", `{"role":"admin"}`, user.RoleAdmin},
		{http.MethodPost, "/batch", `{"userIds":["target"],"role":"user"}`, user.RoleUser},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || db.User.GetX(t.Context(), "target").Role != tc.role {
			t.Fatalf("%s returned %d without saving the role", tc.path, resp.StatusCode)
		}
	}
}
