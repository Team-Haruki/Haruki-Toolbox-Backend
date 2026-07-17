package redis

import (
	"context"
	"testing"
)

func TestGetQueryHash(t *testing.T) {
	t.Parallel()

	if got := getQueryHash(""); got != emptyQueryHash {
		t.Fatalf("getQueryHash(empty) = %q, want %q", got, emptyQueryHash)
	}
	if got := getQueryHash("key=upload_time"); got != "b6715d065478a9abd37d540714b8b78d" {
		t.Fatalf("getQueryHash(value) = %q, want fixed md5 hash", got)
	}
}

func TestBuildCacheKey(t *testing.T) {
	t.Parallel()

	if got := buildCacheKey("ns", "/a/b", ""); got != "ns:/a/b:query=none" {
		t.Fatalf("buildCacheKey without query = %q", got)
	}
	if got := buildCacheKey("ns", "/a/b", "x=1"); got != "ns:/a/b:query=a255512f9d61a6777bd5a304235bd26d" {
		t.Fatalf("buildCacheKey with query = %q", got)
	}
}

func TestBuildGameDataCacheKey(t *testing.T) {
	t.Parallel()

	got := BuildGameDataCacheKey("public", "jp", "suite", 123, " upload_time ")
	want := "game_data:public:jp:suite:123:query=b6715d065478a9abd37d540714b8b78d"
	if got != want {
		t.Fatalf("BuildGameDataCacheKey = %q, want %q", got, want)
	}

	if got := BuildGameDataCacheKey("private", "jp", "mysekai", 123, ""); got != "game_data:private:jp:mysekai:123:query=none" {
		t.Fatalf("BuildGameDataCacheKey empty query = %q", got)
	}

	keyAC := BuildGameDataCacheKey("oauth2", "jp", "suite", 123, "a,c")
	keyCA := BuildGameDataCacheKey("oauth2", "jp", "suite", 123, "c,a")
	if keyAC == keyCA {
		t.Fatalf("different key order should produce different cache keys, both = %q", keyAC)
	}
}

func TestBuildVersionedGameDataCacheKey(t *testing.T) {
	t.Parallel()

	got := BuildVersionedGameDataCacheKey("public", "jp", "suite", 123, "", 1752600000)
	want := BuildGameDataCacheKey("public", "jp", "suite", 123, "") + ":v=1752600000"
	if got != want {
		t.Fatalf("versioned key = %q, want %q", got, want)
	}
	if a, b := BuildVersionedGameDataCacheKey("public", "jp", "suite", 123, "", 1), BuildVersionedGameDataCacheKey("public", "jp", "suite", 123, "", 2); a == b {
		t.Fatalf("different generations must produce different keys")
	}
}

func TestBuildGameDataStampMemoKey(t *testing.T) {
	t.Parallel()

	if got := BuildGameDataStampMemoKey("jp", "suite", 123); got != "game_data:stamp:jp:suite:123" {
		t.Fatalf("memo key = %q", got)
	}
}

func TestClearCacheDeletesStampMemo(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	bodyKey := BuildVersionedGameDataCacheKey("public", "jp", "suite", 321, "", 1752600000)
	memoKey := BuildGameDataStampMemoKey("jp", "suite", 321)
	if err := srv.Set(bodyKey, "cached-body"); err != nil {
		t.Fatalf("seed body: %v", err)
	}
	if err := srv.Set(memoKey, "1752600000"); err != nil {
		t.Fatalf("seed memo: %v", err)
	}

	if err := manager.ClearCache(ctx, "suite", "jp", 321); err != nil {
		t.Fatalf("ClearCache error: %v", err)
	}
	if srv.Exists(bodyKey) {
		t.Fatalf("versioned body key survived ClearCache")
	}
	if srv.Exists(memoKey) {
		t.Fatalf("stamp memo key survived ClearCache")
	}
}
