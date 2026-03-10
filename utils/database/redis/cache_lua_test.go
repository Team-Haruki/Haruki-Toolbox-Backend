package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newTestRedisManager(t *testing.T) (*HarukiRedisManager, *miniredis.Miniredis) {
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

	return &HarukiRedisManager{Redis: client}, srv
}

func TestIncrementWithTTL(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	key := "rate-limit:test-key"

	count, err := manager.IncrementWithTTL(ctx, key, 2*time.Second)
	if err != nil {
		t.Fatalf("IncrementWithTTL first call error: %v", err)
	}
	if count != 1 {
		t.Fatalf("first count = %d, want 1", count)
	}

	srv.FastForward(1 * time.Second)
	count, err = manager.IncrementWithTTL(ctx, key, 10*time.Second)
	if err != nil {
		t.Fatalf("IncrementWithTTL second call error: %v", err)
	}
	if count != 2 {
		t.Fatalf("second count = %d, want 2", count)
	}

	ttl := srv.TTL(key)
	if ttl > 2*time.Second {
		t.Fatalf("ttl was unexpectedly extended, ttl = %v", ttl)
	}
	if ttl <= 0 {
		t.Fatalf("ttl should remain positive, ttl = %v", ttl)
	}
}

func TestDeleteCacheIfValueMatches(t *testing.T) {
	t.Parallel()

	manager, _ := newTestRedisManager(t)
	ctx := context.Background()
	key := "oauth2:code:test"

	if err := manager.Redis.Set(ctx, key, "payload-v1", time.Minute).Err(); err != nil {
		t.Fatalf("seed redis value error: %v", err)
	}

	deleted, err := manager.DeleteCacheIfValueMatches(ctx, key, "payload-v2")
	if err != nil {
		t.Fatalf("DeleteCacheIfValueMatches mismatch error: %v", err)
	}
	if deleted {
		t.Fatalf("expected mismatch to not delete value")
	}

	val, err := manager.Redis.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("redis get after mismatch error: %v", err)
	}
	if val != "payload-v1" {
		t.Fatalf("value after mismatch = %q, want payload-v1", val)
	}

	deleted, err = manager.DeleteCacheIfValueMatches(ctx, key, "payload-v1")
	if err != nil {
		t.Fatalf("DeleteCacheIfValueMatches match error: %v", err)
	}
	if !deleted {
		t.Fatalf("expected exact match to delete value")
	}

	exists, err := manager.Redis.Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("redis exists check error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected key to be removed, exists = %d", exists)
	}
}

func TestGetRawCache(t *testing.T) {
	t.Parallel()

	manager, _ := newTestRedisManager(t)
	ctx := context.Background()
	key := "oauth2:code:raw"

	got, found, err := manager.GetRawCache(ctx, key)
	if err != nil {
		t.Fatalf("GetRawCache missing key error: %v", err)
	}
	if found {
		t.Fatalf("expected missing key to return found=false, got %q", got)
	}

	if err := manager.Redis.Set(ctx, key, `{"code":"abc"}`, time.Minute).Err(); err != nil {
		t.Fatalf("seed redis value error: %v", err)
	}

	got, found, err = manager.GetRawCache(ctx, key)
	if err != nil {
		t.Fatalf("GetRawCache existing key error: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true for existing key")
	}
	if got != `{"code":"abc"}` {
		t.Fatalf("GetRawCache value = %q, want %q", got, `{"code":"abc"}`)
	}
}

func TestClearCacheRemovesAllPublicQueryVariants(t *testing.T) {
	t.Parallel()

	manager, _ := newTestRedisManager(t)
	ctx := context.Background()

	// Same path with different query hashes should all be invalidated.
	keys := []string{
		buildCacheKey(publicAccessNamespace, "/public/jp/suite/123", ""),
		buildCacheKey(publicAccessNamespace, "/public/jp/suite/123", "key=upload_time"),
		buildCacheKey(publicAccessNamespace, "/public/jp/suite/123", "key=userProfile"),
	}
	for _, key := range keys {
		if err := manager.Redis.Set(ctx, key, "v", time.Minute).Err(); err != nil {
			t.Fatalf("seed redis value error: %v", err)
		}
	}

	untouchedKey := buildCacheKey(publicAccessNamespace, "/public/jp/suite/999", "key=userProfile")
	if err := manager.Redis.Set(ctx, untouchedKey, "keep", time.Minute).Err(); err != nil {
		t.Fatalf("seed untouched redis value error: %v", err)
	}

	if err := manager.ClearCache(ctx, "suite", "jp", 123); err != nil {
		t.Fatalf("ClearCache returned error: %v", err)
	}

	for _, key := range keys {
		exists, err := manager.Redis.Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("redis exists check error for %s: %v", key, err)
		}
		if exists != 0 {
			t.Fatalf("expected key %s to be removed", key)
		}
	}

	untouchedVal, err := manager.Redis.Get(ctx, untouchedKey).Result()
	if err != nil {
		t.Fatalf("redis get untouched key error: %v", err)
	}
	if untouchedVal != "keep" {
		t.Fatalf("untouched key value = %q, want %q", untouchedVal, "keep")
	}
}

func TestSetCachesAtomically(t *testing.T) {
	t.Parallel()

	manager, _ := newTestRedisManager(t)
	ctx := context.Background()

	items := []CacheItem{
		{Key: "batch:key:1", Value: "value-1"},
		{Key: "batch:key:2", Value: "value-2"},
		{Key: "batch:key:3", Value: 123},
	}
	if err := manager.SetCachesAtomically(ctx, items, time.Minute); err != nil {
		t.Fatalf("SetCachesAtomically error: %v", err)
	}

	var got1 string
	found, err := manager.GetCache(ctx, "batch:key:1", &got1)
	if err != nil {
		t.Fatalf("GetCache key1 error: %v", err)
	}
	if !found || got1 != "value-1" {
		t.Fatalf("key1 found/value = %v/%q, want true/value-1", found, got1)
	}

	var got2 string
	found, err = manager.GetCache(ctx, "batch:key:2", &got2)
	if err != nil {
		t.Fatalf("GetCache key2 error: %v", err)
	}
	if !found || got2 != "value-2" {
		t.Fatalf("key2 found/value = %v/%q, want true/value-2", found, got2)
	}

	var got3 int
	found, err = manager.GetCache(ctx, "batch:key:3", &got3)
	if err != nil {
		t.Fatalf("GetCache key3 error: %v", err)
	}
	if !found || got3 != 123 {
		t.Fatalf("key3 found/value = %v/%d, want true/123", found, got3)
	}
}

func TestSetCachesAtomicallyMarshalFailureDoesNotWrite(t *testing.T) {
	t.Parallel()

	manager, _ := newTestRedisManager(t)
	ctx := context.Background()

	items := []CacheItem{
		{Key: "batch:ok", Value: "ok"},
		{Key: "batch:bad", Value: func() {}},
	}
	if err := manager.SetCachesAtomically(ctx, items, time.Minute); err == nil {
		t.Fatalf("SetCachesAtomically should fail for unsupported value")
	}

	exists, err := manager.Redis.Exists(ctx, "batch:ok", "batch:bad").Result()
	if err != nil {
		t.Fatalf("redis exists check error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected no keys to be written on marshal failure, exists=%d", exists)
	}
}
