package upload

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

// TestIOSUploadChunkStoreConcurrentPersistAccounting guards the atomicity of the
// persist script: size accounting must stay exact under concurrent writers with
// no process-level lock (the pre-Lua implementation needed a global mutex for
// this, which also serialized unrelated users). Each goroutine persists a
// distinct chunk of a shared upload; afterwards the stored size must equal the
// exact sum of chunk lengths and the chunk count must equal the writer count.
func TestIOSUploadChunkStoreConcurrentPersistAccounting(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 424242, "upload-id")

	const totalChunks = 16
	chunk := []byte("0123456789abcdef")
	var wg sync.WaitGroup
	errs := make([]error, totalChunks)
	for i := 0; i < totalChunks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = persistIOSUploadChunk(ctx, client, uploadKey, totalChunks, idx, chunk)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("persistIOSUploadChunk(%d) returned error: %v", i, err)
		}
	}

	metaKey, chunkIndexKey, _ := iosUploadRedisKeys(uploadKey)
	rawSize, err := client.HGet(ctx, metaKey, "size").Result()
	if err != nil {
		t.Fatalf("HGet(size) returned error: %v", err)
	}
	size, err := strconv.ParseInt(rawSize, 10, 64)
	if err != nil {
		t.Fatalf("parse stored size %q: %v", rawSize, err)
	}
	if want := int64(totalChunks * len(chunk)); size != want {
		t.Fatalf("stored size = %d, want %d", size, want)
	}
	count, err := client.HLen(ctx, chunkIndexKey).Result()
	if err != nil {
		t.Fatalf("HLen returned error: %v", err)
	}
	if count != totalChunks {
		t.Fatalf("chunk count = %d, want %d", count, totalChunks)
	}
}

func TestValidateDataUploadHeader(t *testing.T) {
	t.Parallel()

	valid := &dataUploadHeader{
		UploadId:    "u-123",
		ChunkIndex:  0,
		TotalChunks: 1,
	}
	if err := validateDataUploadHeader(valid); err != nil {
		t.Fatalf("validateDataUploadHeader(valid) returned error: %v", err)
	}

	tests := []struct {
		name   string
		header dataUploadHeader
	}{
		{
			name: "missing upload id",
			header: dataUploadHeader{
				UploadId:    " ",
				ChunkIndex:  0,
				TotalChunks: 1,
			},
		},
		{
			name: "upload id too long",
			header: dataUploadHeader{
				UploadId:    string(make([]byte, maxUploadIDLength+1)),
				ChunkIndex:  0,
				TotalChunks: 1,
			},
		},
		{
			name: "total chunks too small",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  0,
				TotalChunks: 0,
			},
		},
		{
			name: "total chunks too large",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  0,
				TotalChunks: maxUploadChunkCount + 1,
			},
		},
		{
			name: "negative chunk index",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  -1,
				TotalChunks: 2,
			},
		},
		{
			name: "chunk index out of range",
			header: dataUploadHeader{
				UploadId:    "u",
				ChunkIndex:  2,
				TotalChunks: 2,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateDataUploadHeader(&tc.header); err == nil {
				t.Fatalf("validateDataUploadHeader(%s) should fail", tc.name)
			}
		})
	}
}

func TestBuildChunkUploadKey(t *testing.T) {
	t.Parallel()

	key := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")
	if key != "toolbox-user|jp|123456|upload-id" {
		t.Fatalf("unexpected upload key: %q", key)
	}
}

func TestIOSUploadChunkStoreLifecycle(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")

	first, err := persistIOSUploadChunk(ctx, client, uploadKey, 2, 0, []byte("ab"))
	if err != nil {
		t.Fatalf("persistIOSUploadChunk(first) returned error: %v", err)
	}
	if first.State != iosUploadChunkStateIncomplete || first.Count != 1 || first.Size != 2 {
		t.Fatalf("first persist result = %#v", first)
	}

	second, err := persistIOSUploadChunk(ctx, client, uploadKey, 2, 1, []byte("cd"))
	if err != nil {
		t.Fatalf("persistIOSUploadChunk(second) returned error: %v", err)
	}
	if second.State != iosUploadChunkStateCompleteClaimed || second.Count != 2 || second.Size != 4 {
		t.Fatalf("second persist result = %#v", second)
	}

	chunks, err := loadIOSUploadChunks(ctx, client, uploadKey, 2)
	if err != nil {
		t.Fatalf("loadIOSUploadChunks returned error: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(chunks))
	}
	if got := string(chunks[0].Data) + string(chunks[1].Data); got != "abcd" {
		t.Fatalf("unexpected chunk payloads: %#v", chunks)
	}

	if err := clearIOSUploadChunks(ctx, client, uploadKey, 2); err != nil {
		t.Fatalf("clearIOSUploadChunks returned error: %v", err)
	}

	metaKey, chunkIndexKey, claimKey := iosUploadRedisKeys(uploadKey)
	keys := append([]string{metaKey, chunkIndexKey, claimKey}, iosUploadChunkPartKeys(uploadKey, 2)...)
	exists, err := client.Exists(ctx, keys...).Result()
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected upload keys to be cleared")
	}
}

func TestIOSUploadChunkStoreRejectsInconsistentTotals(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")

	if _, err := persistIOSUploadChunk(ctx, client, uploadKey, 2, 0, []byte("ab")); err != nil {
		t.Fatalf("persistIOSUploadChunk returned error: %v", err)
	}
	result, err := persistIOSUploadChunk(ctx, client, uploadKey, 3, 1, []byte("cd"))
	if err != nil {
		t.Fatalf("persistIOSUploadChunk returned error: %v", err)
	}
	if result.State != iosUploadChunkStateInconsistentTotal {
		t.Fatalf("state = %d, want %d", result.State, iosUploadChunkStateInconsistentTotal)
	}
}

func TestIOSUploadChunkStoreClaimsCompletionOnce(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")

	first, err := persistIOSUploadChunk(ctx, client, uploadKey, 1, 0, []byte("ab"))
	if err != nil {
		t.Fatalf("persistIOSUploadChunk returned error: %v", err)
	}
	if first.State != iosUploadChunkStateCompleteClaimed {
		t.Fatalf("first state = %d, want %d", first.State, iosUploadChunkStateCompleteClaimed)
	}

	second, err := persistIOSUploadChunk(ctx, client, uploadKey, 1, 0, []byte("ab"))
	if err != nil {
		t.Fatalf("persistIOSUploadChunk returned error: %v", err)
	}
	if second.State != iosUploadChunkStateCompleteAlreadyClaimed {
		t.Fatalf("second state = %d, want %d", second.State, iosUploadChunkStateCompleteAlreadyClaimed)
	}
}

func TestIOSUploadChunkStoreEnforcesMaxSize(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")
	metaKey, _, _ := iosUploadRedisKeys(uploadKey)

	if err := client.HSet(ctx, metaKey, map[string]any{
		"total": 2,
		"size":  maxDataChunksSize - 1,
	}).Err(); err != nil {
		t.Fatalf("HSet returned error: %v", err)
	}

	result, err := persistIOSUploadChunk(ctx, client, uploadKey, 2, 1, []byte("ab"))
	if err != nil {
		t.Fatalf("persistIOSUploadChunk returned error: %v", err)
	}
	if result.State != iosUploadChunkStateTooLarge {
		t.Fatalf("state = %d, want %d", result.State, iosUploadChunkStateTooLarge)
	}
}

func newIOSUploadRedisClient(t *testing.T) *goredis.Client {
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
	return client
}

// Chunks may arrive in any order; assembly must return them in index order, each
// read from its own key so no single Redis reply carries the whole upload.
func TestIOSUploadChunkStoreLoadsPartsInIndexOrder(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")

	const total = 5
	var last iosUploadChunkPersistResult
	for _, idx := range []int{3, 0, 4, 2, 1} {
		var err error
		last, err = persistIOSUploadChunk(ctx, client, uploadKey, total, idx, []byte{byte('a' + idx)})
		if err != nil {
			t.Fatalf("persistIOSUploadChunk(%d) returned error: %v", idx, err)
		}
	}
	if last.State != iosUploadChunkStateCompleteClaimed || last.Count != total || last.Size != total {
		t.Fatalf("final persist result = %#v", last)
	}
	for i, key := range iosUploadChunkPartKeys(uploadKey, total) {
		kind, err := client.Type(ctx, key).Result()
		if err != nil || kind != "string" {
			t.Fatalf("chunk %d stored as %q (err %v), want its own string key", i, kind, err)
		}
		if ttl := client.PTTL(ctx, key).Val(); ttl <= 0 || ttl > iosChunkPartTTL {
			t.Fatalf("chunk %d TTL = %s, want within (0, %s]", i, ttl, iosChunkPartTTL)
		}
	}

	chunks, err := loadIOSUploadChunks(ctx, client, uploadKey, total)
	if err != nil {
		t.Fatalf("loadIOSUploadChunks returned error: %v", err)
	}
	for i, chunk := range chunks {
		if chunk.ChunkIndex != i || string(chunk.Data) != string(rune('a'+i)) {
			t.Fatalf("chunk %d = %#v", i, chunk)
		}
	}
}

func TestIOSUploadChunkStoreResendReplacesChunkSize(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")

	if _, err := persistIOSUploadChunk(ctx, client, uploadKey, 2, 0, []byte("abcd")); err != nil {
		t.Fatal(err)
	}
	result, err := persistIOSUploadChunk(ctx, client, uploadKey, 2, 0, []byte("xy"))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != iosUploadChunkStateIncomplete || result.Count != 1 || result.Size != 2 {
		t.Fatalf("resent chunk result = %#v, want the old length replaced", result)
	}
}

func TestIOSUploadChunkStoreReportsMissingPart(t *testing.T) {
	t.Parallel()

	client := newIOSUploadRedisClient(t)
	ctx := context.Background()
	uploadKey := buildChunkUploadKey("toolbox-user", harukiUtils.SupportedDataUploadServerJP, 123456, "upload-id")

	for idx := range 2 {
		if _, err := persistIOSUploadChunk(ctx, client, uploadKey, 2, idx, []byte("ab")); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.Del(ctx, harukiRedis.BuildIOSUploadChunkPartKey(uploadKey, 1)).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadIOSUploadChunks(ctx, client, uploadKey, 2); err == nil || !strings.Contains(err.Error(), "chunk 1 of 2 is missing") {
		t.Fatalf("loadIOSUploadChunks error = %v, want the missing chunk reported", err)
	}
	if _, err := loadIOSUploadChunks(ctx, client, uploadKey, 0); err == nil {
		t.Fatal("loadIOSUploadChunks accepted a zero chunk count")
	}
}

func TestIOSUploadChunkStoreNilClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if _, err := persistIOSUploadChunk(ctx, nil, "k", 1, 0, []byte("a")); err == nil {
		t.Fatal("persist accepted a nil client")
	}
	if _, err := loadIOSUploadChunks(ctx, nil, "k", 1); err == nil {
		t.Fatal("load accepted a nil client")
	}
	if err := clearIOSUploadChunks(ctx, nil, "k", 1); err == nil {
		t.Fatal("clear accepted a nil client")
	}
	if err := resetIOSUploadClaim(ctx, nil, "k"); err == nil {
		t.Fatal("reset accepted a nil client")
	}
}
