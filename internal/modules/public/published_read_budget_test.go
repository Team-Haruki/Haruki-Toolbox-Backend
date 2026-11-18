package public

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestPublicReadAdmitsAuthorizedRequestsBeforeServingVersionedCache(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, "sqlite3", "file:published-read-budget?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	db.User.Create().SetID("owner").SetName("Owner").SetEmail("owner@example.test").ExecX(ctx)
	db.GameAccountBinding.Create().SetUserID("owner").SetServer("jp").SetGameUserID("123").SetVerified(true).
		SetMysekai(&harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: true}).ExecX(ctx)

	cache := miniredis.RunT(t)
	port, err := strconv.Atoi(cache.Port())
	if err != nil {
		t.Fatal(err)
	}
	redis := harukiRedis.NewRedisClient(harukiConfig.RedisConfig{Host: cache.Host(), Port: port}, "test-sign-token")
	defer redis.Close()
	cache.Set(harukiRedis.BuildGameDataStampMemoKey("jp", "mysekai", 123), "100")
	dba := &database.HarukiToolboxDBManager{DB: db, Redis: redis}
	key := harukiRedis.BuildVersionedGameDataCacheKey("public", "jp", "mysekai", 123, "", 100, dba.GameData.HarvestSchemaFingerprint("jp"))
	cache.Set(key, `{"legacy":true}`)
	cache.Set(key+data.PublishedGameDataCacheVersion, `{"example":true}`)

	app := fiber.New()
	app.Get("/public/:server/:data_type/:user_id", handlePublicDataRequest(&harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: dba}))
	check := func(query string, want int) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/public/jp/mysekai/123"+query, nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("GET %s returned %d, want %d", query, resp.StatusCode, want)
		}
	}
	check("?key=sample,sample", http.StatusBadRequest)
	func() {
		for range 4 {
			release, ok := data.ReservePublishedGameDataRead()
			if !ok {
				t.Fatal("could not reserve published read budget")
			}
			defer release()
		}
		check("", http.StatusServiceUnavailable)
	}()
	check("", http.StatusOK)
	cache.Del(key + data.PublishedGameDataCacheVersion)
	check("", http.StatusInternalServerError)
}
