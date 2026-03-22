package upload

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiRedis "haruki-suite/utils/database/redis"
	"strconv"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const iosChunkUploadTTL = 5 * time.Minute

var iosUploadChunkStoreMu sync.Mutex

const (
	iosUploadChunkStateIncomplete int64 = iota
	iosUploadChunkStateCompleteClaimed
	iosUploadChunkStateCompleteAlreadyClaimed
	iosUploadChunkStateInconsistentTotal = -1
	iosUploadChunkStateTooLarge          = -2
)

type iosUploadChunkPersistResult struct {
	State int64
	Count int
	Size  int64
}

func iosUploadRedisKeys(uploadKey string) (metaKey string, chunkDataKey string, claimKey string) {
	return harukiRedis.BuildIOSUploadChunkMetaKey(uploadKey),
		harukiRedis.BuildIOSUploadChunkDataKey(uploadKey),
		harukiRedis.BuildIOSUploadChunkClaimKey(uploadKey)
}

func persistIOSUploadChunk(
	ctx context.Context,
	redisClient *goredis.Client,
	uploadKey string,
	totalChunks int,
	chunkIndex int,
	chunkData []byte,
) (iosUploadChunkPersistResult, error) {
	if redisClient == nil {
		return iosUploadChunkPersistResult{}, fmt.Errorf("redis client is nil")
	}

	metaKey, chunkDataKey, claimKey := iosUploadRedisKeys(uploadKey)
	chunkIndexField := strconv.Itoa(chunkIndex)

	iosUploadChunkStoreMu.Lock()
	defer iosUploadChunkStoreMu.Unlock()

	meta, err := redisClient.HGetAll(ctx, metaKey).Result()
	if err != nil {
		return iosUploadChunkPersistResult{}, err
	}

	currentSize := int64(0)
	if rawTotal, ok := meta["total"]; ok && rawTotal != "" {
		expectedTotal, parseErr := strconv.Atoi(rawTotal)
		if parseErr != nil {
			return iosUploadChunkPersistResult{}, fmt.Errorf("parse stored total chunks: %w", parseErr)
		}
		if expectedTotal != totalChunks {
			return iosUploadChunkPersistResult{State: iosUploadChunkStateInconsistentTotal}, nil
		}
	}
	if rawSize, ok := meta["size"]; ok && rawSize != "" {
		parsedSize, parseErr := strconv.ParseInt(rawSize, 10, 64)
		if parseErr != nil {
			return iosUploadChunkPersistResult{}, fmt.Errorf("parse stored upload size: %w", parseErr)
		}
		currentSize = parsedSize
	}

	oldChunkData, err := redisClient.HGet(ctx, chunkDataKey, chunkIndexField).Bytes()
	if err != nil && err != goredis.Nil {
		return iosUploadChunkPersistResult{}, err
	}
	if err == goredis.Nil {
		oldChunkData = nil
	}

	newSize := currentSize - int64(len(oldChunkData)) + int64(len(chunkData))
	if newSize > maxDataChunksSize {
		count, countErr := redisClient.HLen(ctx, chunkDataKey).Result()
		if countErr != nil {
			return iosUploadChunkPersistResult{}, countErr
		}
		return iosUploadChunkPersistResult{
			State: iosUploadChunkStateTooLarge,
			Count: int(count),
			Size:  currentSize,
		}, nil
	}

	pipe := redisClient.TxPipeline()
	pipe.HSet(ctx, metaKey, map[string]any{
		"total": totalChunks,
		"size":  newSize,
	})
	pipe.HSet(ctx, chunkDataKey, chunkIndexField, chunkData)
	pipe.Expire(ctx, metaKey, iosChunkUploadTTL)
	pipe.Expire(ctx, chunkDataKey, iosChunkUploadTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return iosUploadChunkPersistResult{}, err
	}

	count, err := redisClient.HLen(ctx, chunkDataKey).Result()
	if err != nil {
		return iosUploadChunkPersistResult{}, err
	}
	if int(count) != totalChunks {
		if err := redisClient.Del(ctx, claimKey).Err(); err != nil {
			return iosUploadChunkPersistResult{}, err
		}
		return iosUploadChunkPersistResult{
			State: iosUploadChunkStateIncomplete,
			Count: int(count),
			Size:  newSize,
		}, nil
	}

	setStatus, err := redisClient.SetArgs(ctx, claimKey, "1", goredis.SetArgs{
		Mode: "NX",
		TTL:  iosChunkUploadTTL,
	}).Result()
	if err != nil && err != goredis.Nil {
		return iosUploadChunkPersistResult{}, err
	}
	claimed := err == nil && setStatus == "OK"
	state := iosUploadChunkStateCompleteAlreadyClaimed
	if claimed {
		state = iosUploadChunkStateCompleteClaimed
	}
	return iosUploadChunkPersistResult{
		State: state,
		Count: int(count),
		Size:  newSize,
	}, nil
}

func loadIOSUploadChunks(
	ctx context.Context,
	redisClient *goredis.Client,
	uploadKey string,
	totalChunks int,
) ([]harukiUtils.DataChunk, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("redis client is nil")
	}

	_, chunkDataKey, _ := iosUploadRedisKeys(uploadKey)

	iosUploadChunkStoreMu.Lock()
	defer iosUploadChunkStoreMu.Unlock()

	rawChunks, err := redisClient.HGetAll(ctx, chunkDataKey).Result()
	if err != nil {
		return nil, err
	}
	if len(rawChunks) != totalChunks {
		return nil, fmt.Errorf("chunk count mismatch: got %d want %d", len(rawChunks), totalChunks)
	}

	chunks := make([]harukiUtils.DataChunk, 0, len(rawChunks))
	for rawIndex, rawData := range rawChunks {
		chunkIndex, err := strconv.Atoi(rawIndex)
		if err != nil {
			return nil, fmt.Errorf("parse chunk index %q: %w", rawIndex, err)
		}
		if chunkIndex < 0 || chunkIndex >= totalChunks {
			return nil, fmt.Errorf("chunk index %d out of range", chunkIndex)
		}
		chunks = append(chunks, harukiUtils.DataChunk{
			ChunkIndex: chunkIndex,
			Data:       []byte(rawData),
		})
	}
	return chunks, nil
}

func clearIOSUploadChunks(ctx context.Context, redisClient *goredis.Client, uploadKey string) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is nil")
	}

	metaKey, chunkDataKey, claimKey := iosUploadRedisKeys(uploadKey)
	return redisClient.Del(ctx, metaKey, chunkDataKey, claimKey).Err()
}

func resetIOSUploadClaim(ctx context.Context, redisClient *goredis.Client, uploadKey string) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is nil")
	}

	_, _, claimKey := iosUploadRedisKeys(uploadKey)
	return redisClient.Del(ctx, claimKey).Err()
}
