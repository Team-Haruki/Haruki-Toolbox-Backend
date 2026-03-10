package adminusers

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newAdminUserIntegrationSessionHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *harukiRedis.HarukiRedisManager, context.Context) {
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

	redisManager := &harukiRedis.HarukiRedisManager{Redis: client}
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager:      &database.HarukiToolboxDBManager{Redis: redisManager},
		SessionHandler: harukiAPIHelper.NewSessionHandler(client, "local-sign-key"),
	}
	return helper, redisManager, context.Background()
}

func TestClearManagedUserSessionsClearsLocalSessions(t *testing.T) {
	helper, redisManager, ctx := newAdminUserIntegrationSessionHelper(t)

	if err := redisManager.Redis.Set(ctx, "u1:s1", "1", time.Minute).Err(); err != nil {
		t.Fatalf("seed key u1:s1 failed: %v", err)
	}
	if err := redisManager.Redis.Set(ctx, "u1:s2", "1", time.Minute).Err(); err != nil {
		t.Fatalf("seed key u1:s2 failed: %v", err)
	}
	if err := redisManager.Redis.Set(ctx, "u2:s1", "1", time.Minute).Err(); err != nil {
		t.Fatalf("seed key u2:s1 failed: %v", err)
	}

	sessionClearFailed := clearManagedUserSessions(ctx, helper, "u1", nil)
	if sessionClearFailed {
		t.Fatalf("sessionClearFailed = true, want false")
	}

	keys, err := redisManager.Redis.Keys(ctx, "u1:*").Result()
	if err != nil {
		t.Fatalf("query keys for u1 failed: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected u1 sessions to be cleared, got keys=%v", keys)
	}

	keys, err = redisManager.Redis.Keys(ctx, "u2:*").Result()
	if err != nil {
		t.Fatalf("query keys for u2 failed: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected u2 sessions to remain, got keys=%v", keys)
	}
}

func TestClearManagedUserSessionsReportsFailureWhenRedisUnavailable(t *testing.T) {
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, "local-sign-key"),
	}
	sessionClearFailed := clearManagedUserSessions(context.Background(), helper, "u1", nil)
	if !sessionClearFailed {
		t.Fatalf("sessionClearFailed = false, want true")
	}
}
