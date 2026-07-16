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

	stamp, err := ResolveGameDataStamp(context.Background(), helper, "jp", "suite", 123)
	if err != nil {
		t.Fatalf("ResolveGameDataStamp error: %v", err)
	}
	if stamp != 1752600000 {
		t.Fatalf("stamp = %d, want 1752600000", stamp)
	}
}

func TestResolveGameDataStampMemoNegativeCache(t *testing.T) {
	helper, srv := newStampTestHelper(t)
	memoKey := harukiRedis.BuildGameDataStampMemoKey("jp", "mysekai", 456)
	if err := srv.Set(memoKey, "0"); err != nil {
		t.Fatalf("seed memo: %v", err)
	}

	stamp, err := ResolveGameDataStamp(context.Background(), helper, "jp", "mysekai", 456)
	if err != nil {
		t.Fatalf("ResolveGameDataStamp error: %v", err)
	}
	if stamp != 0 {
		t.Fatalf("stamp = %d, want 0 (memoized missing document)", stamp)
	}
}

func TestResolveGameDataStampCorruptMemoFallsThrough(t *testing.T) {
	helper, srv := newStampTestHelper(t)
	memoKey := harukiRedis.BuildGameDataStampMemoKey("jp", "suite", 789)
	if err := srv.Set(memoKey, "not-a-number"); err != nil {
		t.Fatalf("seed memo: %v", err)
	}

	// Mongo is nil here, so falling through must surface an error rather than
	// trusting the corrupt memo.
	if _, err := ResolveGameDataStamp(context.Background(), helper, "jp", "suite", 789); err == nil {
		t.Fatalf("expected error when memo is corrupt and Mongo is unavailable")
	}
}
