package userprivateapi

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

// The profile's miss path against a real game-data PostgreSQL:
//
//	GAMEDATA_HANDLER_TEST_PG=postgres://...   (a disposable database)
func newPrivatePostgresEnv(t *testing.T) (*privateTestEnv, *gamedata.Service) {
	t.Helper()
	dsn := os.Getenv("GAMEDATA_HANDLER_TEST_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_HANDLER_TEST_PG to a disposable database")
	}
	ctx := context.Background()
	pool, err := gamedata.NewPool(ctx, gamedata.PoolConfig{URL: dsn, MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+catalog.QuoteIdent(catalog.Suite().Table)); err != nil {
		t.Fatal(err)
	}
	if _, err := gamedata.EnsureSchema(ctx, pool, true, catalog.Suite()); err != nil {
		t.Fatal(err)
	}
	service := gamedata.NewService(pool)
	return newPrivateTestEnv(t, service), service
}

func profileUpload(uploadTime int64, cardID int) map[string]any {
	return map[string]any{
		"upload_time": uploadTime,
		"userGamedata": map[string]any{
			"userId": ownedGameUserID, "name": "n", "deck": 1, "exp": 2, "totalExp": 3, "coin": 4, "rank": 5,
			"secretToken": "must-not-leak",
		},
		"userCards":              []any{map[string]any{"cardId": cardID}},
		"userCostume3dStatuses":  []any{map[string]any{"costume3dId": 1, "status": "have"}},
		"userCostume3dShopItems": []any{map[string]any{"costume3dShopItemId": 2, "status": "bought"}},
		"userBrandNewKey":        []any{map[string]any{"id": 7}},
	}
}

func decodeBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}
	return m
}

func TestPrivateProfileRendersCachesAndInvalidates(t *testing.T) {
	env, service := newPrivatePostgresEnv(t)
	ctx := context.Background()
	stamp := time.Now().Add(-time.Hour).Unix()
	if _, err := service.Suite().Write(ctx, ownedGameUserID, "jp", profileUpload(stamp, 1), gamedata.WriteSuite, gamedata.DefaultLimits()); err != nil {
		t.Fatal(err)
	}

	resp, body := env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud"), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	got := decodeBody(t, body)
	for _, denied := range []string{"userCostume3dStatuses", "userCostume3dShopItems"} {
		if _, present := got[denied]; present {
			t.Fatalf("denied key %s served", denied)
		}
	}
	for _, kept := range []string{"_id", "_idString", "server", "upload_time", "userCards", "userBrandNewKey", "userGamedata"} {
		if _, present := got[kept]; !present {
			t.Fatalf("%s missing from profile body: %s", kept, body)
		}
	}
	if strings.Contains(body, "must-not-leak") {
		t.Fatal("userGamedata is not filtered in the profile body")
	}
	if _, present := got["userDecks"]; present {
		t.Fatal("a key absent from the row was rendered")
	}

	// The full body still carries the costume keys.
	_, fullBody := env.get(t, privatePath(ownedGameUserID, "100", ""), nil)
	if !strings.Contains(fullBody, "userCostume3dStatuses") {
		t.Fatal("full body lost the costume keys")
	}

	// One canonical entry, with the full body's 7-day TTL.
	key := env.cacheKey(cloudProfile, ownedGameUserID, stamp)
	if !env.redis.Exists(key) {
		t.Fatal("profile body was not cached under its canonical key")
	}
	if ttl := env.redis.TTL(key); ttl != harukiRedis.GameDataCacheTTL {
		t.Fatalf("profile TTL = %s, want %s", ttl, harukiRedis.GameDataCacheTTL)
	}
	// The loader's write indexes the profile body, which is what lets the
	// upload clear below reach it without a keyspace scan.
	if ok, err := env.redis.SIsMember(harukiRedis.BuildGameDataBodyIndexKey("jp", "suite", ownedGameUserID), key); err != nil || !ok {
		t.Fatalf("profile body is not in the document's cache index (err %v)", err)
	}

	// A hit is served from the cache: a change written behind the upload path
	// (same upload_time, no cache clear) is not visible.
	code, _ := catalog.ServerCode("jp")
	if _, err := service.Pool().Exec(ctx, `UPDATE game_suite SET user_cards_j = '[{"cardId":99}]' WHERE user_id = $1 AND server = $2`, ownedGameUserID, code); err != nil {
		t.Fatal(err)
	}
	if _, cached := env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud"), nil); cached != body {
		t.Fatal("second request was not served from the cache")
	}

	// An upload clears the cache and moves the generation, like a full body.
	next := stamp + 60
	if _, err := service.Suite().Write(ctx, ownedGameUserID, "jp", profileUpload(next, 2), gamedata.WriteSuite, gamedata.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if err := env.helper.DBManager.Redis.ClearUploadedGameDataCaches(ctx, "suite", "jp", ownedGameUserID); err != nil {
		t.Fatal(err)
	}
	if env.redis.Exists(key) {
		t.Fatal("upload did not clear the profile body")
	}
	_, fresh := env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud"), nil)
	if !strings.Contains(fresh, `"cardId":2`) || !strings.Contains(fresh, fmt.Sprintf(`"upload_time":%d`, next)) {
		t.Fatalf("profile body after upload is stale: %s", fresh)
	}

	// And the conditional read answers 304 for the new generation.
	resp, _ = env.get(t, privatePath(ownedGameUserID, "100", "profile=cloud&known_upload_time="+strconv.FormatInt(next, 10)), nil)
	if resp.StatusCode != 304 {
		t.Fatalf("known_upload_time: status %d, want 304", resp.StatusCode)
	}
}

// A missing row is 404 for the profile exactly as for the full body.
func TestPrivateProfileMissingRowIsNotFound(t *testing.T) {
	env, _ := newPrivatePostgresEnv(t)
	for _, extra := range []string{"", "profile=cloud"} {
		if resp, _ := env.get(t, privatePath(ownedGameUserID, "100", extra), nil); resp.StatusCode != 404 {
			t.Fatalf("%q: status %d, want 404", extra, resp.StatusCode)
		}
	}
}
