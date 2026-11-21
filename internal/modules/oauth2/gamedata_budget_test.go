package oauth2

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/gofiber/fiber/v3"
)

func TestGameDataOAuthReadChecksSelectionAndUsesBoundedCache(t *testing.T) {
	env := newDeviceTestEnv(t)
	ctx := context.Background()
	env.db.DB.User.Create().SetID("owner").SetName("Owner").SetEmail("owner@example.test").ExecX(ctx)
	env.db.DB.GameAccountBinding.Create().SetUserID("owner").SetServer("jp").SetGameUserID("123").SetVerified(true).ExecX(ctx)
	env.redis.Set(harukiRedis.BuildGameDataStampMemoKey("jp", "mysekai", 123), "100")
	key := harukiRedis.BuildVersionedGameDataCacheKey("oauth2", "jp", "mysekai", 123, "", 100, env.db.GameData.HarvestSchemaFingerprint("jp"))
	allowedKeys := []string{"isEnabled"}
	env.apiHelper.RuntimeConfig = runtimeconfig.New(runtimeconfig.Snapshot{AllowedKeys: allowedKeys}, nil)
	cachePrefix := key + data.PublishedGameDataCacheVersion
	currentKey := cachePrefix + ":mysekai-full"
	env.redis.Set(key, `{"legacy":true}`)
	env.redis.Set(cachePrefix, `{"legacy":true}`)
	env.redis.Set(cachePrefix+":a="+data.PublicAllowlistDigest([]string{"isEnabled", "updatedResources"}), `{"isEnabled":true}`)
	const currentBody = `{"isEnabled":true,"updatedResources":{"userMysekaiItems":[1]},"fixtureUnlistedField":true}`
	env.redis.Set(currentKey, currentBody)
	env.app.Get("/game/:server/:data_type/:user_id", func(c fiber.Ctx) error {
		c.Locals("userID", "owner")
		return c.Next()
	}, handleOAuth2GetGameData(env.apiHelper))
	unsupported, err := env.app.Test(httptest.NewRequest(http.MethodGet, "/game/jp/mysekai_birthday_party/123", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = unsupported.Body.Close()
	if unsupported.StatusCode != http.StatusBadRequest {
		t.Fatalf("upload-only Mysekai type returned %d on the OAuth2 read route", unsupported.StatusCode)
	}

	check := func(query string, want int) {
		t.Helper()
		resp, err := env.app.Test(httptest.NewRequest(http.MethodGet, "/game/jp/mysekai/123"+query, nil))
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
				t.Fatalf("GET %s returned %q, %v, want %s", query, body, err, currentBody)
			}
		}
	}
	check("?key=sample,sample", http.StatusBadRequest)
	check("?key=updatedResources.items", http.StatusBadRequest)
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
	env.apiHelper.SetAllowedKeys([]string{"upload_time"})
	check("", http.StatusOK)
	env.redis.Del(currentKey)
	check("", http.StatusInternalServerError)
	env.redis.Set(harukiRedis.BuildGameDataStampMemoKey("jp", "mysekai", 123), "0")
	check("?known_upload_time=100", http.StatusInternalServerError)
}

func TestOAuth2SuiteKeepsAllowlistAndCachedProjection(t *testing.T) {
	env := newDeviceTestEnv(t)
	ctx := t.Context()
	env.db.DB.User.Create().SetID("owner").SetName("Owner").SetEmail("owner@example.test").ExecX(ctx)
	env.db.DB.GameAccountBinding.Create().SetUserID("owner").SetServer("jp").SetGameUserID("123").SetVerified(true).ExecX(ctx)
	allowed := []string{"upload_time", "userCards"}
	env.apiHelper.SetAllowedKeys(allowed)
	env.redis.Set(harukiRedis.BuildGameDataStampMemoKey("jp", "suite", 123), "100")
	key := harukiRedis.BuildVersionedGameDataCacheKey("oauth2", "jp", "suite", 123, "", 100, env.db.GameData.HarvestSchemaFingerprint("jp"))
	const cached = `{"upload_time":100,"userCards":[]}`
	env.redis.Set(key+data.PublishedGameDataCacheVersion+":a="+data.PublicAllowlistDigest(allowed), cached)
	env.app.Get("/game/:server/:data_type/:user_id", func(c fiber.Ctx) error {
		c.Locals("userID", "owner")
		return c.Next()
	}, handleOAuth2GetGameData(env.apiHelper))

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", http.StatusOK},
		{"?known_upload_time=100", http.StatusNotModified},
		{"?key=updatedResources&known_upload_time=100", http.StatusBadRequest},
	} {
		resp, err := env.app.Test(httptest.NewRequest(http.MethodGet, "/game/jp/suite/123"+tc.query, nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != tc.want || tc.want == http.StatusOK && string(body) != cached {
			t.Fatalf("GET %s returned %d %s, %v, want %d", tc.query, resp.StatusCode, body, err, tc.want)
		}
	}
}

func TestHydraPublicProxyRejectsUnsupportedRepresentations(t *testing.T) {
	env := newDeviceTestEnv(t)
	const endpoint = "/oauth2/revoke"
	env.app.Post("/test-proxy", handleHydraPublicProxy(env.hydraConfig, endpoint))
	for _, tc := range []struct {
		name, encoding, body string
		want                 int
	}{
		{"compressed", "gzip", "example", http.StatusUnsupportedMediaType},
		{"above form limit", "identity", strings.Repeat("a", (64<<10)+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test-proxy", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Content-Encoding", tc.encoding)
			resp, err := env.app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
	if calls := env.hydra.callsTo(http.MethodPost, endpoint); len(calls) != 0 {
		t.Fatalf("forwarded %d rejected requests", len(calls))
	}
}
