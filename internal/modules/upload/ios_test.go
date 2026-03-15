package upload

import (
	"context"
	harukiUtils "haruki-suite/utils"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

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
	if got := string(chunks[0].Data) + string(chunks[1].Data); got != "abcd" && got != "cdab" {
		t.Fatalf("unexpected chunk payloads: %#v", chunks)
	}

	if err := clearIOSUploadChunks(ctx, client, uploadKey); err != nil {
		t.Fatalf("clearIOSUploadChunks returned error: %v", err)
	}

	metaKey, chunkDataKey, claimKey := iosUploadRedisKeys(uploadKey)
	exists, err := client.Exists(ctx, metaKey, chunkDataKey, claimKey).Result()
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
