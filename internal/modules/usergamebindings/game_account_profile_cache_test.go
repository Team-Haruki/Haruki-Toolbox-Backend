package usergamebindings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekaiapi"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

// profileUpstream fakes the Sekai API profile endpoint per game UID.
type profileUpstream struct {
	mu       sync.Mutex
	statuses map[string]int
	words    map[string]string
	delay    time.Duration
	hits     atomic.Int64
}

func (u *profileUpstream) set(uid string, status int, word string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.statuses[uid] = status
	u.words[uid] = word
}

func (u *profileUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.hits.Add(1)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // api/<server>/<uid>/profile
	uid := parts[2]
	u.mu.Lock()
	status, word, delay := u.statuses[uid], u.words[uid], u.delay
	u.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if status == 0 {
		status = http.StatusNotFound
	}
	w.WriteHeader(status)
	if status == http.StatusOK {
		_, _ = fmt.Fprintf(w, `{"userProfile":{"userId":%s,"word":%q}}`, uid, word)
	}
}

type profileTestEnv struct {
	app      *fiber.App
	helper   *api.HarukiToolboxRouterHelpers
	upstream *profileUpstream
	redis    *miniredis.Miniredis
}

func newProfileTestEnv(t *testing.T, options sekaiapi.Options) *profileTestEnv {
	t.Helper()
	db := enttest.Open(t, "sqlite3", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, id := range []string{"owner", "grantee", "stranger"} {
		if _, err := db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []struct{ server, uid string }{{"tw", "123"}, {"tw", "456"}, {"jp", "789"}} {
		if _, err := db.GameAccountBinding.Create().SetServer(binding.server).SetGameUserID(binding.uid).SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// The grantee may read suite data, not the profile.
	if _, err := db.GameAccountDataGrant.Create().SetOwnerUserID("owner").SetGranteeUserID("grantee").
		SetServer("tw").SetGameUserID("123").SetDataType("suite").SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx); err != nil {
		t.Fatal(err)
	}

	upstream := &profileUpstream{statuses: map[string]int{}, words: map[string]string{}}
	upstream.set("123", http.StatusOK, "hello")
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)

	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	helper := &api.HarukiToolboxRouterHelpers{
		DBManager:      &database.HarukiToolboxDBManager{DB: db, Redis: &harukiRedis.HarukiRedisManager{Redis: rdb}},
		SekaiAPIClient: sekaiapi.NewHarukiSekaiAPIClientWithOptions(srv.URL, "token", options),
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", c.Get("X-Test-User"))
		return c.Next()
	})
	app.Get("/:server/:game_user_id/:data_type", handleGetOwnedGameAccountData(helper))
	return &profileTestEnv{app: app, helper: helper, upstream: upstream, redis: mr}
}

func (e *profileTestEnv) get(t *testing.T, user, uid string) (int, string, http.Header) {
	t.Helper()
	return e.getOn(t, "tw", user, uid)
}

func (e *profileTestEnv) getOn(t *testing.T, server, user, uid string) (int, string, http.Header) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodGet, "/"+server+"/"+uid+"/profile", nil)
	req.Header.Set("X-Test-User", user)
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

func TestOwnedProfileCacheIsReadOnlyAfterAuthorization(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{})
	cacheKey := harukiRedis.BuildSekaiAPIProfileCacheKey("tw", "123")

	status, body, _ := env.get(t, "owner", "123")
	if status != fiber.StatusOK || !strings.Contains(body, `"word":"hello"`) {
		t.Fatalf("owner view = %d %s", status, body)
	}
	if env.upstream.hits.Load() != 1 {
		t.Fatalf("upstream hits = %d, want 1", env.upstream.hits.Load())
	}
	if ttl := env.redis.TTL(cacheKey); ttl != sekaiapi.DefaultProfileCacheTTL {
		t.Fatalf("cache TTL = %s, want %s", ttl, sekaiapi.DefaultProfileCacheTTL)
	}

	// The profile is cached now; callers without profile access must still be
	// refused, and must not receive it.
	for _, user := range []string{"stranger", "grantee", ""} {
		status, body, _ := env.get(t, user, "123")
		if status == fiber.StatusOK || strings.Contains(body, "hello") {
			t.Fatalf("%q read a cached profile: %d %s", user, status, body)
		}
	}

	// The owner's repeat view is served from the cache.
	env.upstream.set("123", http.StatusOK, "changed")
	status, body, _ = env.get(t, "owner", "123")
	if status != fiber.StatusOK || !strings.Contains(body, `"word":"hello"`) {
		t.Fatalf("cached view = %d %s", status, body)
	}
	if env.upstream.hits.Load() != 1 {
		t.Fatalf("cached view reached the upstream: hits = %d", env.upstream.hits.Load())
	}

	// Binding verification needs the profile comment as it is now: never cached.
	if err := verifyGameAccountOwnership(context.Background(), env.helper, "123", "tw", "changed"); err != nil {
		t.Fatalf("verify read a stale profile: %v", err)
	}
	if env.upstream.hits.Load() != 2 {
		t.Fatalf("verify did not call the upstream: hits = %d", env.upstream.hits.Load())
	}
}

func TestOwnedProfileErrorsAreNotCached(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{})
	cacheKey := harukiRedis.BuildSekaiAPIProfileCacheKey("tw", "456")

	for _, tc := range []struct {
		status     int
		wantStatus int
		wantBody   string
	}{
		{http.StatusNotFound, fiber.StatusNotFound, "game account not found"},
		{http.StatusInternalServerError, fiber.StatusBadGateway, "game server unavailable"},
		{http.StatusServiceUnavailable, fiber.StatusBadGateway, "game server unavailable"},
	} {
		env.upstream.set("456", tc.status, "")
		status, body, _ := env.get(t, "owner", "456")
		if status != tc.wantStatus || !strings.Contains(body, tc.wantBody) {
			t.Fatalf("upstream %d -> %d %s", tc.status, status, body)
		}
		if env.redis.Exists(cacheKey) {
			t.Fatalf("upstream %d response was cached", tc.status)
		}
	}
}

// An open breaker keeps today's 502 "game server unavailable" answer and adds
// Retry-After, without calling the upstream.
func TestOwnedProfileBreakerKeepsResponseShape(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{})
	if status, _, _ := env.get(t, "owner", "123"); status != fiber.StatusOK {
		t.Fatalf("warm-up view = %d", status)
	}
	env.upstream.set("456", http.StatusInternalServerError, "")
	for range 5 {
		env.get(t, "owner", "456")
	}
	hits := env.upstream.hits.Load()

	status, body, header := env.get(t, "owner", "456")
	if status != fiber.StatusBadGateway || !strings.Contains(body, "game server unavailable") {
		t.Fatalf("open breaker = %d %s", status, body)
	}
	if header.Get(fiber.HeaderRetryAfter) == "" {
		t.Fatal("open breaker response has no Retry-After")
	}
	if env.upstream.hits.Load() != hits {
		t.Fatal("open breaker reached the upstream")
	}
	// A cached profile still serves while the breaker is open.
	if status, body, _ := env.get(t, "owner", "123"); status != fiber.StatusOK || !strings.Contains(body, "hello") {
		t.Fatalf("cached view while open = %d %s", status, body)
	}
	// Breakers are per server.
	env.upstream.set("789", http.StatusOK, "jp")
	if status, _, _ := env.getOn(t, "jp", "owner", "789"); status != fiber.StatusOK {
		t.Fatalf("jp view while tw is open = %d", status)
	}
}

func TestOwnedProfileViewTimeout(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{ProfileViewTimeoutByServer: map[string]time.Duration{
		"jp": 30 * time.Millisecond,
		"tw": time.Nanosecond,
	}})
	env.upstream.set("789", http.StatusOK, "jp")
	env.upstream.delay = 300 * time.Millisecond

	start := time.Now()
	status, body, _ := env.getOn(t, "jp", "owner", "789")
	if status != fiber.StatusBadGateway || !strings.Contains(body, "failed to query game account profile") {
		t.Fatalf("timed-out view = %d %s", status, body)
	}
	if elapsed := time.Since(start); elapsed >= 300*time.Millisecond {
		t.Fatalf("view waited %s for a %s deadline", elapsed, 30*time.Millisecond)
	}
	// TW is never bounded below 5 s, so the same delay succeeds there.
	if status, body, _ := env.get(t, "owner", "123"); status != fiber.StatusOK {
		t.Fatalf("tw view under the floor = %d %s", status, body)
	}
}

func TestOwnedProfileWithoutCache(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{ProfileCacheTTL: -1})
	for range 2 {
		if status, _, _ := env.get(t, "owner", "123"); status != fiber.StatusOK {
			t.Fatalf("status = %d", status)
		}
	}
	if env.upstream.hits.Load() != 2 || env.redis.Exists(harukiRedis.BuildSekaiAPIProfileCacheKey("tw", "123")) {
		t.Fatalf("disabled cache still cached: hits = %d", env.upstream.hits.Load())
	}

	// No Redis at all behaves the same.
	env.helper.DBManager.Redis = nil
	if status, _, _ := env.get(t, "owner", "123"); status != fiber.StatusOK {
		t.Fatalf("status without redis = %d", status)
	}
}

func TestOwnedProfileConcurrentViewsShareOneCall(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{})
	env.upstream.delay = 200 * time.Millisecond
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if status, _, _ := env.get(t, "owner", "123"); status != fiber.StatusOK {
				t.Errorf("status = %d", status)
			}
		})
	}
	wg.Wait()
	if hits := env.upstream.hits.Load(); hits != 1 {
		t.Fatalf("concurrent views made %d upstream calls, want 1", hits)
	}
}

func TestVerifyOwnershipUsesItsOwnDeadline(t *testing.T) {
	env := newProfileTestEnv(t, sekaiapi.Options{VerifyTimeout: 30 * time.Millisecond})
	env.upstream.delay = time.Second
	start := time.Now()
	err := verifyGameAccountOwnership(context.Background(), env.helper, "123", "tw", "hello")
	if !errors.Is(err, errGameAccountProfileRequestFailed) {
		t.Fatalf("err = %v, want a request failure", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("verify waited %s, past its deadline", elapsed)
	}
}
