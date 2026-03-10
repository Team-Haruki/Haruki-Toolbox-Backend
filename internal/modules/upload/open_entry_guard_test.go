package upload

import (
	"context"
	"haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestOpenUploadRateLimiterAllow(t *testing.T) {
	t.Parallel()

	limiter := newOpenUploadRateLimiter(2, time.Minute)
	now := time.Unix(1700000000, 0).UTC()
	key := "127.0.0.1|POST|/inherit"

	if !limiter.allow(now, key) {
		t.Fatalf("first request should pass")
	}
	if !limiter.allow(now.Add(10*time.Second), key) {
		t.Fatalf("second request should pass")
	}
	if limiter.allow(now.Add(20*time.Second), key) {
		t.Fatalf("third request in same window should be blocked")
	}
	if !limiter.allow(now.Add(61*time.Second), key) {
		t.Fatalf("request after window reset should pass")
	}
}

func TestOpenUploadRateLimiterIsolatedByKey(t *testing.T) {
	t.Parallel()

	limiter := newOpenUploadRateLimiter(1, time.Minute)
	now := time.Unix(1700000000, 0).UTC()

	if !limiter.allow(now, "ip-a|POST|/inherit") {
		t.Fatalf("first key should pass")
	}
	if !limiter.allow(now, "ip-b|POST|/inherit") {
		t.Fatalf("different key should pass")
	}
	if limiter.allow(now, "ip-a|POST|/inherit") {
		t.Fatalf("same key should be blocked after limit")
	}
}

func TestConsumeOpenUploadRateLimit(t *testing.T) {
	t.Parallel()

	apiHelper := newTestAPIHelperWithRedis(t)
	now := time.Unix(1700000000, 0).UTC()
	key := "127.0.0.1|POST|/inherit/:server/:upload_type"

	for i := 0; i < openUploadRateLimitPerMinute; i++ {
		allowed, retryAfter, err := consumeOpenUploadRateLimit(context.Background(), apiHelper, key, now)
		if err != nil {
			t.Fatalf("consumeOpenUploadRateLimit error on request %d: %v", i+1, err)
		}
		if !allowed {
			t.Fatalf("request %d should be allowed, retryAfter=%d", i+1, retryAfter)
		}
	}

	allowed, retryAfter, err := consumeOpenUploadRateLimit(context.Background(), apiHelper, key, now)
	if err != nil {
		t.Fatalf("consumeOpenUploadRateLimit over-limit error: %v", err)
	}
	if allowed {
		t.Fatalf("request above limit should be blocked")
	}
	if retryAfter <= 0 {
		t.Fatalf("retryAfter should be positive when blocked, got %d", retryAfter)
	}

	allowed, retryAfter, err = consumeOpenUploadRateLimit(context.Background(), apiHelper, key, now.Add(openUploadRateLimitWindow))
	if err != nil {
		t.Fatalf("consumeOpenUploadRateLimit after window error: %v", err)
	}
	if !allowed {
		t.Fatalf("request in new window should be allowed, retryAfter=%d", retryAfter)
	}
}

func TestConsumeOpenUploadRateLimitRedisMissing(t *testing.T) {
	t.Parallel()

	allowed, retryAfter, err := consumeOpenUploadRateLimit(context.Background(), &api.HarukiToolboxRouterHelpers{}, "k", time.Now().UTC())
	if err == nil {
		t.Fatalf("expected redis initialization error")
	}
	if allowed {
		t.Fatalf("allowed should be false on redis initialization error")
	}
	if retryAfter != 0 {
		t.Fatalf("retryAfter should be 0 on redis initialization error, got %d", retryAfter)
	}
}

func TestSecondsUntilWindowResetRoundsUpSubSecond(t *testing.T) {
	t.Parallel()

	now := time.Unix(0, int64(time.Minute-time.Millisecond))
	retryAfter := secondsUntilWindowReset(now, time.Minute)
	if retryAfter != 1 {
		t.Fatalf("retryAfter = %d, want 1", retryAfter)
	}
}

func newTestAPIHelperWithRedis(t *testing.T) *api.HarukiToolboxRouterHelpers {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
	})

	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})

	return &api.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			Redis: &harukiRedis.HarukiRedisManager{Redis: client},
		},
	}
}
