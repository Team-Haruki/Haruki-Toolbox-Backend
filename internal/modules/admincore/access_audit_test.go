package admincore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
	goredis "github.com/redis/go-redis/v9"
)

func TestAdminAccessDenialsAreAuditedOncePerActorPerWindow(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	cache := miniredis.RunT(t)
	redis := goredis.NewClient(&goredis.Options{Addr: cache.Addr()})
	defer redis.Close()
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: &harukiRedis.HarukiRedisManager{Redis: redis}}}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", c.Get("X-Test-Actor"))
		return c.Next()
	}, RequireAnyRoleWithLookup(helper, func(fiber.Ctx, string) (string, bool, error) {
		return RoleUser, false, nil
	}, RoleAdmin))
	deny := func(actor, path string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Test-Actor", actor)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("denial status = %d", resp.StatusCode)
		}
	}
	for _, path := range []string{"/settings", "/users", "/logs"} {
		deny("first", path)
	}
	deny("second", "/logs")
	query := db.SystemLog.Query().Where(systemlog.ActionEQ(adminAuditActionAccess))
	if got := query.Clone().CountX(t.Context()); got != 2 {
		t.Fatalf("audit count = %d, want 2", got)
	}
	cache.FastForward(accessDeniedAuditWindow)
	deny("first", "/logs")
	if got := query.CountX(t.Context()); got != 3 {
		t.Fatalf("post-expiry audit count = %d, want 3", got)
	}
}

func TestAdminDenialAuditFallbackHasOneSharedSlot(t *testing.T) {
	previous := fallbackAccessDeniedAuditUntil.Swap(0)
	t.Cleanup(func() { fallbackAccessDeniedAuditUntil.Store(previous) })
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{}}
	if !admitAccessDeniedAudit(t.Context(), helper, "first", adminFailureReasonInsufficientPermissions) {
		t.Fatal("fallback denied its representative event")
	}
	if admitAccessDeniedAudit(t.Context(), helper, "second", adminFailureReasonBannedUserDenied) {
		t.Fatal("fallback admitted extra writes while Redis was unavailable")
	}
}
