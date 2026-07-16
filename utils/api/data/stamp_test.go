package data

import (
	"context"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiDatabase "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newStampTestHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(srv.Close)
	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	dbm := harukiDatabase.NewHarukiToolboxDBManager(nil, &harukiRedis.HarukiRedisManager{Redis: client}, nil)
	return &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: dbm}, srv
}

// The Mongo manager is nil in these tests, so a memo hit passing proves the
// resolver short-circuits on Redis without touching Mongo.
func TestResolveGameDataStampMemoHit(t *testing.T) {
	helper, srv := newStampTestHelper(t)
	memoKey := harukiRedis.BuildGameDataStampMemoKey("jp", "suite", 123)
	if err := srv.Set(memoKey, "1752600000"); err != nil {
		t.Fatalf("seed memo: %v", err)
	}

	stamp, confirmed, err := ResolveGameDataStamp(context.Background(), helper, "jp", "suite", 123)
	if err != nil {
		t.Fatalf("ResolveGameDataStamp error: %v", err)
	}
	if stamp != 1752600000 || !confirmed {
		t.Fatalf("stamp = %d confirmed = %v, want 1752600000 confirmed", stamp, confirmed)
	}
}

// A Mongo outage (nil manager here) with a fallback stamp present must return
// the last-known stamp unconfirmed, so warm generations keep serving but no
// 304 is ever answered from it.
func TestResolveGameDataStampFallbackOnMongoError(t *testing.T) {
	helper, srv := newStampTestHelper(t)
	fallbackKey := harukiRedis.BuildGameDataStampFallbackKey("jp", "suite", 321)
	if err := srv.Set(fallbackKey, "1752500000"); err != nil {
		t.Fatalf("seed fallback: %v", err)
	}

	stamp, confirmed, err := ResolveGameDataStamp(context.Background(), helper, "jp", "suite", 321)
	if err != nil {
		t.Fatalf("ResolveGameDataStamp error: %v", err)
	}
	if stamp != 1752500000 || confirmed {
		t.Fatalf("stamp = %d confirmed = %v, want 1752500000 unconfirmed", stamp, confirmed)
	}
}

func TestResolveGameDataStampMemoNegativeCache(t *testing.T) {
	helper, srv := newStampTestHelper(t)
	memoKey := harukiRedis.BuildGameDataStampMemoKey("jp", "mysekai", 456)
	if err := srv.Set(memoKey, "0"); err != nil {
		t.Fatalf("seed memo: %v", err)
	}

	stamp, confirmed, err := ResolveGameDataStamp(context.Background(), helper, "jp", "mysekai", 456)
	if err != nil {
		t.Fatalf("ResolveGameDataStamp error: %v", err)
	}
	if stamp != 0 || !confirmed {
		t.Fatalf("stamp = %d confirmed = %v, want 0 confirmed (memoized missing document)", stamp, confirmed)
	}
}

func TestResolveGameDataStampCorruptMemoFallsThrough(t *testing.T) {
	helper, srv := newStampTestHelper(t)
	memoKey := harukiRedis.BuildGameDataStampMemoKey("jp", "suite", 789)
	if err := srv.Set(memoKey, "not-a-number"); err != nil {
		t.Fatalf("seed memo: %v", err)
	}

	// Mongo is nil and no fallback stamp exists, so falling through must
	// surface an error rather than trusting the corrupt memo.
	if _, _, err := ResolveGameDataStamp(context.Background(), helper, "jp", "suite", 789); err == nil {
		t.Fatalf("expected error when memo is corrupt and Mongo is unavailable")
	}
}
