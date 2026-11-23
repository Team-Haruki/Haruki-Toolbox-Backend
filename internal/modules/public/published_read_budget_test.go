package public

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func newPublicReadTest(t *testing.T) (*fiber.App, *harukiAPIHelper.HarukiToolboxRouterHelpers, *miniredis.Miniredis) {
	t.Helper()
	ctx := t.Context()
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { db.Close() })
	db.User.Create().SetID("owner").SetName("Owner").SetEmail("owner@example.test").ExecX(ctx)
	db.GameAccountBinding.Create().SetUserID("owner").SetServer("jp").SetGameUserID("123").SetVerified(true).
		SetSuite(&harukiSchema.SuiteDataPrivacySettings{AllowPublicApi: true}).
		SetMysekai(&harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: true}).ExecX(ctx)

	cache := miniredis.RunT(t)
	port, err := strconv.Atoi(cache.Port())
	if err != nil {
		t.Fatal(err)
	}
	redis := harukiRedis.NewRedisClient(harukiConfig.RedisConfig{Host: cache.Host(), Port: port}, "test-sign-token")
	t.Cleanup(func() { redis.Close() })
	app := fiber.New()
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		Router:        app,
		DBManager:     &database.HarukiToolboxDBManager{DB: db, Redis: redis},
		RuntimeConfig: runtimeconfig.New(runtimeconfig.Snapshot{AllowedKeys: []string{"upload_time"}}, nil),
	}
	RegisterPublicRoutes(helper)
	return app, helper, cache
}

func TestPublicReadAdmitsAuthorizedRequestsBeforeServingVersionedCache(t *testing.T) {
	app, helper, cache := newPublicReadTest(t)
	cache.Set(harukiRedis.BuildGameDataStampMemoKey("jp", "mysekai", 123), "100")
	key := harukiRedis.BuildVersionedGameDataCacheKey("public", "jp", "mysekai", 123, "", 100, helper.DBManager.GameData.HarvestSchemaFingerprint("jp"))
	cachePrefix := key + data.PublishedGameDataCacheVersion
	currentKey := cachePrefix + ":mysekai-full"
	const currentBody = `{"upload_time":100,"updatedResources":{"userMysekaiItems":[1]},"fixtureUnlistedField":true}`
	cache.Set(key, `{"legacy":true}`)
	cache.Set(cachePrefix, `{"legacy":true}`)
	cache.Set(cachePrefix+":a="+data.PublicAllowlistDigest([]string{"upload_time"}), `{"upload_time":100}`)
	cache.Set(currentKey, currentBody)

	check := func(query, etag string, want int) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/public/jp/mysekai/123"+query, nil)
		if etag != "" {
			req.Header.Set(fiber.HeaderIfNoneMatch, etag)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("GET %s returned %d, want %d", query, resp.StatusCode, want)
		}
		if want == http.StatusOK {
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != currentBody {
				t.Fatalf("GET %s returned %s, %v, want %s", query, body, err, currentBody)
			}
		}
		return resp.Header.Get(fiber.HeaderETag)
	}
	check("?key=sample,sample", "", http.StatusBadRequest)
	func() {
		for range 4 {
			release, ok := data.ReservePublishedGameDataRead()
			if !ok {
				t.Fatal("could not reserve published read budget")
			}
			defer release()
		}
		check("", "", http.StatusServiceUnavailable)
	}()
	etag := check("", "", http.StatusOK)
	if etag == "" {
		t.Fatal("expected a validator for the full Mysekai response")
	}
	check("?known_upload_time=100", etag, http.StatusNotModified)
	helper.DBManager.DB.GameAccountBinding.Update().SetMysekai(&harukiSchema.MysekaiDataPrivacySettings{}).SaveX(t.Context())
	check("", "", http.StatusNotFound)
	check("?known_upload_time=100", etag, http.StatusNotFound)
	helper.DBManager.DB.GameAccountBinding.Update().SetMysekai(&harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: true}).SaveX(t.Context())
	check("", "", http.StatusOK)
	cache.Del(currentKey)
	check("", "", http.StatusInternalServerError)
}

func TestPublicSuiteStillUsesAllowlistedCache(t *testing.T) {
	app, helper, cache := newPublicReadTest(t)
	cache.Set(harukiRedis.BuildGameDataStampMemoKey("jp", "suite", 123), "100")
	key := harukiRedis.BuildVersionedGameDataCacheKey("public", "jp", "suite", 123, "", 100, helper.DBManager.GameData.HarvestSchemaFingerprint("jp"))
	cache.Set(key+data.PublishedGameDataCacheVersion+":a="+data.PublicAllowlistDigest([]string{"upload_time"}), `{"upload_time":100}`)
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", http.StatusOK},
		{"?known_upload_time=100", http.StatusNotModified},
		{"?key=updatedResources", http.StatusNotFound},
	} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/public/jp/suite/123"+tc.query, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != tc.want || tc.want == http.StatusOK && string(body) != `{"upload_time":100}` {
			t.Fatalf("GET %s returned %d %s, %v, want %d", tc.query, resp.StatusCode, body, err, tc.want)
		}
	}
}
