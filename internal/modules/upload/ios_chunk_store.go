package upload

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	goredis "github.com/redis/go-redis/v9"
)

// iosChunkUploadTTL is the sliding lifetime of an upload's meta and index: every
// chunk renews it, so an upload stays open while its chunks keep arriving.
const iosChunkUploadTTL = 5 * time.Minute

// iosChunkPartTTL is the lifetime of one stored chunk body. A chunk cannot renew
// the bodies of chunks stored before it (they are separate keys), so it gets a
// multiple of the sliding window. iOS scripts send all chunks within seconds;
// an upload whose chunks span longer than this fails at assembly with a missing
// chunk, as one idle for longer than iosChunkUploadTTL always has.
const iosChunkPartTTL = 3 * iosChunkUploadTTL

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
// length (the index hash stores it).
//
// Each chunk body is its own string key. Earlier releases kept all bodies in
// one hash, and reading it back with HGETALL returned the whole multi-MB upload
// in one command, blocking Redis for 10-24 ms; assembly now reads the chunks
// with one GET each. Uploads that were in flight across the deploy that changed
// this layout cannot complete (their earlier chunks are in the old hash, which
// expires within iosChunkUploadTTL): the client re-sends the data on its next
// game request.
//
// KEYS[1]=meta hash  KEYS[2]=chunk index hash  KEYS[3]=this chunk's key  KEYS[4]=claim key
// ARGV[1]=totalChunks ARGV[2]=chunkIndex ARGV[3]=chunkData ARGV[4]=maxSize
// ARGV[5]=ttlMs ARGV[6]=partTtlMs
// Reply: {state, count, size} matching iosUploadChunkPersistResult; a stored
// meta or index field that fails tonumber() is treated as absent rather than
// erroring — only this script ever writes those fields.
var persistIOSUploadChunkScript = goredis.NewScript(`
local total = redis.call('HGET', KEYS[1], 'total')
if total and total ~= '' and tonumber(total) ~= tonumber(ARGV[1]) then
  return {-1, 0, 0}
end
local size = tonumber(redis.call('HGET', KEYS[1], 'size') or 0) or 0
local oldLen = tonumber(redis.call('HGET', KEYS[2], ARGV[2]) or 0) or 0
local newLen = string.len(ARGV[3])
local newSize = size - oldLen + newLen
if newSize > tonumber(ARGV[4]) then
  return {-2, redis.call('HLEN', KEYS[2]), size}
end
redis.call('HSET', KEYS[1], 'total', ARGV[1], 'size', tostring(newSize))
redis.call('HSET', KEYS[2], ARGV[2], tostring(newLen))
redis.call('SET', KEYS[3], ARGV[3], 'PX', ARGV[6])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
redis.call('PEXPIRE', KEYS[2], ARGV[5])
local count = redis.call('HLEN', KEYS[2])
if count ~= tonumber(ARGV[1]) then
  redis.call('DEL', KEYS[4])
  return {0, count, newSize}
end
if redis.call('SET', KEYS[4], '1', 'NX', 'PX', ARGV[5]) then
  return {1, count, newSize}
end
return {2, count, newSize}
`)

func iosUploadRedisKeys(uploadKey string) (metaKey string, chunkIndexKey string, claimKey string) {
	return harukiRedis.BuildIOSUploadChunkMetaKey(uploadKey),
		harukiRedis.BuildIOSUploadChunkIndexKey(uploadKey),
		harukiRedis.BuildIOSUploadChunkClaimKey(uploadKey)
}

func iosUploadChunkPartKeys(uploadKey string, totalChunks int) []string {
	keys := make([]string, totalChunks)
	for i := range keys {
		keys[i] = harukiRedis.BuildIOSUploadChunkPartKey(uploadKey, i)
	}
	return keys
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

	metaKey, chunkIndexKey, claimKey := iosUploadRedisKeys(uploadKey)
	vals, err := persistIOSUploadChunkScript.Run(ctx, redisClient,
		[]string{metaKey, chunkIndexKey, harukiRedis.BuildIOSUploadChunkPartKey(uploadKey, chunkIndex), claimKey},
		totalChunks,
		strconv.Itoa(chunkIndex),
		chunkData,
		maxDataChunksSize,
		iosChunkUploadTTL.Milliseconds(),
		iosChunkPartTTL.Milliseconds(),
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

// loadIOSUploadChunks reads every chunk of a claimed upload in index order with
// pipelined GETs, so no single Redis command carries the whole upload. No lock is
// needed: the claim SETNX in the persist script elects a single assembler, and
// the claim is only granted once every chunk is stored.
func loadIOSUploadChunks(
	ctx context.Context,
	redisClient *goredis.Client,
	uploadKey string,
	totalChunks int,
) ([]harukiUtils.DataChunk, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	if totalChunks <= 0 {
		return nil, fmt.Errorf("invalid chunk count %d", totalChunks)
	}

	pipe := redisClient.Pipeline()
	cmds := make([]*goredis.StringCmd, totalChunks)
	for i, key := range iosUploadChunkPartKeys(uploadKey, totalChunks) {
		cmds[i] = pipe.Get(ctx, key)
	}
	// A missing chunk surfaces as redis.Nil on its own command below.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return nil, err
	}

	chunks := make([]harukiUtils.DataChunk, totalChunks)
	for i, cmd := range cmds {
		data, err := cmd.Bytes()
		if errors.Is(err, goredis.Nil) {
			return nil, fmt.Errorf("chunk %d of %d is missing", i, totalChunks)
		}
		if err != nil {
			return nil, err
		}
		chunks[i] = harukiUtils.DataChunk{ChunkIndex: i, Data: data}
	}
	return chunks, nil
}

func clearIOSUploadChunks(ctx context.Context, redisClient *goredis.Client, uploadKey string, totalChunks int) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is nil")
	}

	metaKey, chunkIndexKey, claimKey := iosUploadRedisKeys(uploadKey)
	keys := append([]string{metaKey, chunkIndexKey, claimKey}, iosUploadChunkPartKeys(uploadKey, max(totalChunks, 0))...)
	return redisClient.Unlink(ctx, keys...).Err()
}

func resetIOSUploadClaim(ctx context.Context, redisClient *goredis.Client, uploadKey string) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is nil")
	}

	_, _, claimKey := iosUploadRedisKeys(uploadKey)
	return redisClient.Del(ctx, claimKey).Err()
}
