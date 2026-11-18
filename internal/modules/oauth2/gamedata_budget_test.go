package oauth2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
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
	env.redis.Set(key, `{"legacy":true}`)
	env.redis.Set(key+data.PublishedGameDataCacheVersion, `{"example":true}`)
	env.app.Get("/game/:server/:data_type/:user_id", func(c fiber.Ctx) error {
		c.Locals("userID", "owner")
		return c.Next()
	}, handleOAuth2GetGameData(env.apiHelper))

	check := func(query string, want int) {
		t.Helper()
		resp, err := env.app.Test(httptest.NewRequest(http.MethodGet, "/game/jp/mysekai/123"+query, nil))
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
	env.redis.Del(key + data.PublishedGameDataCacheVersion)
	check("", http.StatusInternalServerError)
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
