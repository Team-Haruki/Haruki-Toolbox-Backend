package redis

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSetGameDataBodyCacheIndexesKey(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	key := BuildVersionedGameDataCacheKey("private", "jp", "suite", 7, "", 100)
	if err := manager.SetGameDataBodyCache(ctx, "jp", "suite", 7, key, "body", KeyedGameDataCacheTTL); err != nil {
		t.Fatalf("SetGameDataBodyCache: %v", err)
	}
	if got, _ := srv.Get(key); got != "body" {
		t.Fatalf("body = %q", got)
	}
	if ttl := srv.TTL(key); ttl != KeyedGameDataCacheTTL {
		t.Fatalf("body TTL = %s, want %s", ttl, KeyedGameDataCacheTTL)
	}
	indexKey := BuildGameDataBodyIndexKey("jp", "suite", 7)
	if indexKey != "game_data:idx:jp:suite:7" {
		t.Fatalf("index key = %q", indexKey)
	}
	if ok, _ := srv.SIsMember(indexKey, key); !ok {
		t.Fatalf("body key missing from the document index")
	}
	// The index must outlive every body it lists.
	if ttl := srv.TTL(indexKey); ttl != GameDataCacheTTL {
		t.Fatalf("index TTL = %s, want %s", ttl, GameDataCacheTTL)
	}
}

// ClearCache must cost the same few commands whatever the keyspace size: the
// SCAN sweep it replaces walked every key on each upload.
func TestClearCacheDoesNotScanTheKeyspace(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	for i := range 2000 {
		if err := srv.Set(fmt.Sprintf("unrelated:%d", i), "x"); err != nil {
			t.Fatal(err)
		}
	}
	// More bodies than one UNLINK batch, across generations and filters.
	var keys []string
	for i := range gameDataClearBatch + 20 {
		key := BuildVersionedGameDataCacheKey("public", "tw", "mysekai", 9, fmt.Sprintf("k%d", i), int64(i))
		keys = append(keys, key)
		if err := manager.SetGameDataBodyCache(ctx, "tw", "mysekai", 9, key, "v", time.Hour); err != nil {
			t.Fatal(err)
		}
	}

	before := srv.CommandCount()
	if err := manager.ClearCache(ctx, "mysekai", "tw", 9); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}
	// MULTI, UNLINK stamps, SMEMBERS, UNLINK index, EXEC, then two body batches.
	if n := srv.CommandCount() - before; n > 8 {
		t.Fatalf("ClearCache issued %d commands, want a constant handful", n)
	}
	for _, key := range keys {
		if srv.Exists(key) {
			t.Fatalf("body %s survived ClearCache", key)
		}
	}
	if srv.Exists(BuildGameDataBodyIndexKey("tw", "mysekai", 9)) {
		t.Fatal("document index survived ClearCache")
	}
	if !srv.Exists("unrelated:0") {
		t.Fatal("ClearCache touched an unrelated key")
	}
}

// A body cached after a clear lands in a fresh index, so the next clear still
// reaches it.
func TestClearCacheThenWriteStartsFreshIndex(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	if err := manager.ClearCache(ctx, "suite", "en", 5); err != nil {
		t.Fatalf("ClearCache on an empty document: %v", err)
	}
	key := BuildVersionedGameDataCacheKey("public", "en", "suite", 5, "", 2)
	if err := manager.SetGameDataBodyCache(ctx, "en", "suite", 5, key, "v", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := manager.ClearCache(ctx, "suite", "en", 5); err != nil {
		t.Fatal(err)
	}
	if srv.Exists(key) {
		t.Fatal("body written after the first clear survived the second")
	}
}

func TestClearNamespaceRemovesBodyIndexes(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	key := BuildVersionedGameDataCacheKey("oauth2", "jp", "suite", 1, "", 1)
	if err := manager.SetGameDataBodyCache(ctx, "jp", "suite", 1, key, "v", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := manager.ClearNamespace(ctx, GameDataNamespace()); err != nil {
		t.Fatal(err)
	}
	if srv.Exists(key) || srv.Exists(BuildGameDataBodyIndexKey("jp", "suite", 1)) {
		t.Fatal("namespace wipe left a body or its index behind")
	}
}

func TestRawCacheBytesRoundTrip(t *testing.T) {
	t.Parallel()

	manager, _ := newTestRedisManager(t)
	ctx := context.Background()
	if _, found, err := manager.GetRawCacheBytes(ctx, "missing"); err != nil || found {
		t.Fatalf("miss = (found %v, err %v)", found, err)
	}
	payload := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0xff}
	if err := manager.SetRawCacheBytes(ctx, "raw:bytes", payload, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, found, err := manager.GetRawCacheBytes(ctx, "raw:bytes")
	if err != nil || !found || string(got) != string(payload) {
		t.Fatalf("GetRawCacheBytes = (%x, %v, %v)", got, found, err)
	}
}

func TestGameDataCacheHelpersRejectNilClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var manager *HarukiRedisManager
	if err := manager.SetGameDataBodyCache(ctx, "jp", "suite", 1, "k", "v", time.Minute); err == nil {
		t.Fatal("SetGameDataBodyCache accepted a nil client")
	}
	if err := manager.ClearCache(ctx, "suite", "jp", 1); err == nil {
		t.Fatal("ClearCache accepted a nil client")
	}
	if _, _, err := manager.GetRawCacheBytes(ctx, "k"); err == nil {
		t.Fatal("GetRawCacheBytes accepted a nil client")
	}
	if err := manager.SetRawCacheBytes(ctx, "k", nil, time.Minute); err == nil {
		t.Fatal("SetRawCacheBytes accepted a nil client")
	}
}

func TestGameDataCacheHelpersSurfaceRedisErrors(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	srv.Close()
	if err := manager.SetGameDataBodyCache(ctx, "jp", "suite", 1, "k", "v", time.Minute); err == nil {
		t.Fatal("SetGameDataBodyCache hid a Redis error")
	}
	if err := manager.ClearCache(ctx, "suite", "jp", 1); err == nil {
		t.Fatal("ClearCache hid a Redis error")
	}
	if _, _, err := manager.GetRawCacheBytes(ctx, "k"); err == nil {
		t.Fatal("GetRawCacheBytes hid a Redis error")
	}
	if err := manager.SetRawCacheBytes(ctx, "k", []byte("v"), time.Minute); err == nil {
		t.Fatal("SetRawCacheBytes hid a Redis error")
	}
}

// Every surface shares the document index, including derived surfaces such as
// the private projection profiles ("private-profile-<name>-<digest>").
func TestClearCacheReachesEverySurface(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	var keys []string
	for _, surface := range []string{"private", "public", "oauth2", "private-profile-cloud-0a1b2c3d"} {
		key := BuildVersionedGameDataCacheKey(surface, "jp", "suite", 11, "", 100)
		keys = append(keys, key)
		if err := manager.SetGameDataBodyCache(ctx, "jp", "suite", 11, key, "v", GameDataCacheTTL); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.ClearUploadedGameDataCaches(ctx, "suite", "jp", 11); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if srv.Exists(key) {
			t.Fatalf("%s survived the upload clear", key)
		}
	}
}

// Writing a body with SetRawCache would leave it out of the index, where no
// clear can reach it, so it is refused.
func TestSetRawCacheRefusesGameDataBodies(t *testing.T) {
	t.Parallel()

	manager, srv := newTestRedisManager(t)
	ctx := context.Background()
	key := BuildVersionedGameDataCacheKey("private-profile-cloud-0a1b2c3d", "jp", "suite", 11, "", 100)
	if err := manager.SetRawCache(ctx, key, "v", time.Minute); err == nil || srv.Exists(key) {
		t.Fatalf("SetRawCache stored an unindexed body (err %v)", err)
	}
	// Stamp keys in the same namespace are still plain raw values.
	memo := BuildGameDataStampMemoKey("jp", "suite", 11)
	if err := manager.SetRawCache(ctx, memo, "100", time.Minute); err != nil {
		t.Fatalf("SetRawCache(stamp memo): %v", err)
	}
}
