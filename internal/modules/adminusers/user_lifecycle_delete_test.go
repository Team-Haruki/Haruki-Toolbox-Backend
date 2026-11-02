package adminusers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/gofiber/fiber/v3"
)

func TestPermanentDeletionRouteReturnsAcknowledgementAndAudit(t *testing.T) {
	helper, _, ctx := newAdminUserIntegrationSessionHelper(t)
	helper.DBManager.DB = newAdminUserTicketNotificationTestHelper(t).DBManager.DB
	db := helper.DBManager.DB
	db.User.Create().SetID("actor").SetName("Admin").SetEmail("actor@example.test").SetRole(userSchema.RoleSuperAdmin).SaveX(ctx)
	db.User.Create().SetID("target").SetName("Target").SetEmail("target@example.test").SaveX(ctx)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { c.Locals("userID", "actor"); return c.Next() })
	RegisterAdminUserRoutes(helper, app.Group("/api/admin"), nil, harukiAPIHelper.UserDataBuilder{})
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/users/target/permanent", strings.NewReader(`{"confirmUserId":"target"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body harukiAPIHelper.GenericResponse[struct {
		UserID  string `json:"userId"`
		Deleted bool   `json:"deleted"`
	}]
	if err := json.UnmarshalRead(resp.Body, &body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || body.UpdatedData == nil || body.UpdatedData.UserID != "target" || !body.UpdatedData.Deleted {
		t.Fatalf("status=%d acknowledgement=%+v", resp.StatusCode, body.UpdatedData)
	}
	if db.User.Query().Where(userSchema.IDEQ("target")).ExistX(ctx) {
		t.Fatal("acknowledged deletion retained account")
	}
	if !db.SystemLog.Query().Where(systemlog.ActionEQ(adminAuditActionUserDelete), systemlog.TargetIDEQ("target"), systemlog.ResultEQ("success")).ExistX(ctx) {
		t.Fatal("missing permanent-deletion audit")
	}
}

func TestDeletionRechecksTargetRoleAfterInitialLookup(t *testing.T) {
	helper, _, ctx := newAdminUserIntegrationSessionHelper(t)
	helper.DBManager.DB = newAdminUserTicketNotificationTestHelper(t).DBManager.DB
	db := helper.DBManager.DB
	db.User.Create().SetID("target").SetName("Target").SetEmail("target@example.test").SetRole(userSchema.RoleSuperAdmin).SaveX(ctx)
	if _, err := permanentlyDeleteUser(ctx, helper, nil, "actor", roleAdmin, "target"); err == nil {
		t.Fatal("ordinary admin deleted a target promoted to super-admin")
	}
	if db.User.GetX(ctx, "target").Banned {
		t.Fatal("guarded deletion changed protected user")
	}
}

func TestPermanentlyDeleteUserCleansBindingsAndIdentity(t *testing.T) {
	for _, identityStatus := range []int{http.StatusNoContent, http.StatusNotFound} {
		t.Run(http.StatusText(identityStatus), func(t *testing.T) {
			helper, manager, ctx := newAdminUserIntegrationSessionHelper(t)
			helper.DBManager.DB = newAdminUserTicketNotificationTestHelper(t).DBManager.DB
			db := helper.DBManager.DB
			for _, id := range []string{"target", "other"} {
				db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").SaveX(ctx)
			}
			db.User.UpdateOneID("target").SetKratosIdentityID("identity-target").SaveX(ctx)
			db.SocialPlatformInfo.Create().SetPlatform("qq").SetPlatformUserID("123456789").SetUserID("target").SetVerified(true).SaveX(ctx)
			db.AuthorizeSocialPlatformInfo.Create().SetPlatform("qqbot").SetPlatformUserID("qid").SetPlatformID(1).SetUserID("target").SaveX(ctx)
			db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetUserID("target").SaveX(ctx)
			db.IOSScriptCode.Create().SetUserID("target").SetUploadCode("test-code").SaveX(ctx)
			for _, pair := range [][2]string{{"target", "other"}, {"other", "target"}} {
				db.GameAccountDataGrant.Create().SetOwnerUserID(pair[0]).SetGranteeUserID(pair[1]).SetServer("jp").SetGameUserID("123").SetDataType("suite").SetExpiresAt(time.Now().Add(time.Hour)).SaveX(ctx)
			}
			db.InheritVaultKey.Create().SetUserID("target").SetKeyVersion(1).SetPublicKey([]byte("public")).SetWrappedPrivateKey([]byte("sealed")).SetWrapScheme("test").SetSecretKind("pin").SetKdfParams("{}").SetMasterKeyVersion(1).SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).SaveX(ctx)
			db.InheritVaultEntry.Create().SetID("entry").SetUserID("target").SetServer("jp").SetGameUserID(123).SetKeyVersion(1).SetWrapScheme("test").SetCredentialSealed([]byte("sealed")).SetCredentialExpiresAt(time.Now().Add(time.Hour)).SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).SaveX(ctx)
			if err := manager.Redis.Set(ctx, "target:session", "session", time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			if err := manager.Redis.Set(ctx, "other:session", "session", time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			identityCalls := 0
			revoked := map[string]bool{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case "/admin/identities/identity-target":
					identityCalls++
					w.WriteHeader(identityStatus)
				case "/admin/oauth2/auth/sessions/consent":
					revoked[r.URL.Query().Get("subject")] = true
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected provider path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			t.Cleanup(server.Close)
			helper.SessionHandler.KratosAdminURL = server.URL
			hydra := oauth2.NewHydraConfig(oauth2.HydraConfigOptions{Provider: oauth2.ProviderHydra, AdminURL: server.URL, RequestTimeout: time.Second})
			bindings, err := permanentlyDeleteUser(ctx, helper, hydra, "admin", roleSuperAdmin, "target")
			if err != nil {
				t.Fatal(err)
			}
			if len(bindings) != 1 || identityCalls != 1 || !revoked["identity-target"] || !revoked["target"] {
				t.Fatalf("cleanup missing: bindings=%d identity=%d revoked=%v", len(bindings), identityCalls, revoked)
			}
			if db.User.Query().CountX(ctx) != 1 || db.User.GetX(ctx, "other").Banned {
				t.Fatal("wrong user deleted or changed")
			}
			counts := []int{db.SocialPlatformInfo.Query().CountX(ctx), db.AuthorizeSocialPlatformInfo.Query().CountX(ctx), db.GameAccountBinding.Query().CountX(ctx), db.GameAccountDataGrant.Query().CountX(ctx), db.IOSScriptCode.Query().CountX(ctx), db.InheritVaultEntry.Query().CountX(ctx), db.InheritVaultKey.Query().CountX(ctx)}
			for _, count := range counts {
				if count != 0 {
					t.Fatalf("account dependencies retained: %v", counts)
				}
			}
			if manager.Redis.Exists(ctx, "target:session", harukiRedis.InheritActiveKeyPrefix+"target").Val() != 0 || manager.Redis.Exists(ctx, "other:session").Val() != 1 {
				t.Fatal("sessions or deletion lock not cleaned correctly")
			}
			// The old QQ can now be attached to another account.
			db.SocialPlatformInfo.Create().SetPlatform("qq").SetPlatformUserID("123456789").SetUserID("other").SaveX(ctx)
		})
	}
}

func TestDeleteUserRejectsUnconfirmedAndProtectedTargets(t *testing.T) {
	for _, tc := range []struct {
		name, actor, role, targetRole, body string
		status                              int
	}{
		{"old-client", "admin", roleAdmin, roleUser, "", 400},
		{"wrong-confirmation", "admin", roleAdmin, roleUser, `{"confirmUserId":"other"}`, 400},
		{"self", "target", roleSuperAdmin, roleSuperAdmin, `{"confirmUserId":"target"}`, 400},
		{"protected", "admin", roleAdmin, roleSuperAdmin, `{"confirmUserId":"target"}`, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			helper := newAdminUserTicketNotificationTestHelper(t)
			helper.DBManager.DB.User.Create().SetID("target").SetName("Target").SetEmail("target@example.test").SetRole(userSchema.Role(tc.targetRole)).SaveX(t.Context())
			app := fiber.New()
			app.Use(func(c fiber.Ctx) error { c.Locals("userID", tc.actor); c.Locals("userRole", tc.role); return c.Next() })
			app.Delete("/users/:target_user_id", handleDeleteUser(helper, nil))
			req := httptest.NewRequest(http.MethodDelete, "/users/target", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.status {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			if helper.DBManager.DB.User.GetX(t.Context(), "target").Banned {
				t.Fatal("rejected deletion changed account")
			}
		})
	}
}

func TestDeleteUserFailureKeepsAccountAndBindings(t *testing.T) {
	helper, manager, ctx := newAdminUserIntegrationSessionHelper(t)
	helper.DBManager.DB = newAdminUserTicketNotificationTestHelper(t).DBManager.DB
	db := helper.DBManager.DB
	db.User.Create().SetID("target").SetName("Target").SetEmail("target@example.test").SetKratosIdentityID("identity-target").SaveX(ctx)
	db.SocialPlatformInfo.Create().SetPlatform("qq").SetPlatformUserID("123456789").SetUserID("target").SaveX(ctx)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(server.Close)
	helper.SessionHandler.KratosAdminURL = server.URL
	if _, err := permanentlyDeleteUser(ctx, helper, nil, "admin", roleSuperAdmin, "target"); err == nil {
		t.Fatal("provider failure was ignored")
	}
	if db.User.GetX(ctx, "target").Banned || db.SocialPlatformInfo.Query().CountX(ctx) != 1 {
		t.Fatal("failed deletion did not roll back")
	}
	if err := manager.Redis.Set(ctx, harukiRedis.InheritActiveKeyPrefix+"target", "running-job", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := permanentlyDeleteUser(ctx, helper, nil, "admin", roleSuperAdmin, "target"); err == nil {
		t.Fatal("deleted account during active transfer")
	}
	if _, err := db.User.Get(ctx, "target"); postgresql.IsNotFound(err) {
		t.Fatal("active account disappeared")
	}
}
