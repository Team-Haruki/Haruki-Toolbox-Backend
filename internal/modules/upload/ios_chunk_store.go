package upload

import (
	"context"
	"fmt"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const iosChunkUploadTTL = 5 * time.Minute

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

// persistIOSUploadChunkScript runs the whole check-then-write persist as one
// atomic server-side operation, replacing a process-global mutex held across
// 5-6 sequential Redis round trips. Atomicity in Redis (instead of a Go lock)
// means: (a) concurrent uploaders no longer serialize behind one slow command
// for ALL users, (b) the size accounting stays correct across multiple backend
// replicas, and (c) the old chunk body is never transferred just to learn its
// length (HSTRLEN is O(1)).
//
// KEYS[1]=meta hash  KEYS[2]=chunk-data hash  KEYS[3]=claim key
// ARGV[1]=totalChunks ARGV[2]=chunkIndex ARGV[3]=chunkData ARGV[4]=maxSize ARGV[5]=ttlMs
// Reply: {state, count, size} matching iosUploadChunkPersistResult; a stored
// meta field that fails tonumber() is treated as inconsistent/absent rather
// than erroring — only this script ever writes those fields.
var persistIOSUploadChunkScript = goredis.NewScript(`
local total = redis.call('HGET', KEYS[1], 'total')
if total and total ~= '' and tonumber(total) ~= tonumber(ARGV[1]) then
  return {-1, 0, 0}
end
local size = tonumber(redis.call('HGET', KEYS[1], 'size') or 0) or 0
local oldLen = redis.call('HSTRLEN', KEYS[2], ARGV[2])
local newSize = size - oldLen + string.len(ARGV[3])
if newSize > tonumber(ARGV[4]) then
  return {-2, redis.call('HLEN', KEYS[2]), size}
end
redis.call('HSET', KEYS[1], 'total', ARGV[1], 'size', tostring(newSize))
redis.call('HSET', KEYS[2], ARGV[2], ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
redis.call('PEXPIRE', KEYS[2], ARGV[5])
local count = redis.call('HLEN', KEYS[2])
if count ~= tonumber(ARGV[1]) then
  redis.call('DEL', KEYS[3])
  return {0, count, newSize}
end
if redis.call('SET', KEYS[3], '1', 'NX', 'PX', ARGV[5]) then
  return {1, count, newSize}
end
return {2, count, newSize}
`)

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
	vals, err := persistIOSUploadChunkScript.Run(ctx, redisClient,
		[]string{metaKey, chunkDataKey, claimKey},
		totalChunks,
		strconv.Itoa(chunkIndex),
		chunkData,
		maxDataChunksSize,
		iosChunkUploadTTL.Milliseconds(),
	).Int64Slice()
	if err != nil {
		return iosUploadChunkPersistResult{}, err
	}
	if len(vals) != 3 {
		return iosUploadChunkPersistResult{}, fmt.Errorf("unexpected persist script reply length %d", len(vals))
	}
	return iosUploadChunkPersistResult{
		State: vals[0],
		Count: int(vals[1]),
		Size:  vals[2],
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

	// No lock needed: the claim SETNX in the persist script elects a single
	// assembler, and HGETALL is atomic so it observes a consistent chunk set.
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
