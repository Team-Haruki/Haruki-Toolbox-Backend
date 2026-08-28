package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
)

const (
	gameDataNamespace = "game_data"
	emptyQueryHash    = "none"

	// GameDataCacheTTL bounds versioned game-data body entries. It is a
	// garbage backstop, not a freshness mechanism: bodies are keyed by the
	// document's upload_time (see BuildVersionedGameDataCacheKey), so a new
	// upload simply moves readers to a new key and stale generations die by
	// TTL, LRU eviction (the production instance runs volatile-lru), or the
	// upload-time cache clear.
	GameDataCacheTTL = 7 * 24 * time.Hour
	// KeyedGameDataCacheTTL bounds bodies materialized for an explicit ?key=
	// filter. Distinct key permutations each mint their own cache entry, so an
	// authorized caller could otherwise accumulate a week of junk generations;
	// a shorter horizon caps that inflation while still covering real polling
	// intervals.
	KeyedGameDataCacheTTL = 6 * time.Hour
	// FreshGenerationWindow / FreshGenerationCacheTTL bound the one race a
	// versioned key plus write fence cannot express: two uploads minted in the
	// same wall-clock second share a stamp, so a body read between their
	// persists can be pinned under the still-current generation. Such a
	// collision is only possible while the generation is young (mint-to-persist
	// lag), so bodies written within FreshGenerationWindow of their stamp get
	// the short TTL — restoring the old 5-minute self-heal bound for exactly
	// the collision class — and stable generations keep the full TTL. The
	// window must cover the slowest mint-to-persist path: the async iOS chunk
	// upload runs its whole pipeline under a 2-minute budget after stamping.
	FreshGenerationWindow   = 3 * time.Minute
	FreshGenerationCacheTTL = 5 * time.Minute
	// GameDataStampMemoTTL bounds the per-document upload_time memo that lets
	// the read path resolve the current cache generation from Redis instead of
	// Mongo. It is the staleness ceiling for every stamp-changing write path
	// (uploads, backfill, binding clears); the residual same-second upload
	// window is fenced separately at cache-write time (see
	// ConfirmGameDataCacheWrite).
	GameDataStampMemoTTL = 60 * time.Second

	redisIncrementWithTTLScript = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`

	redisDeleteIfMatchScript = `
local current = redis.call('GET', KEYS[1])
if not current then
  return 0
end
if current == ARGV[1] or current == ARGV[2] then
  redis.call('DEL', KEYS[1])
  return 1
end
return -1
	`
)

func GameDataNamespace() string {
	return gameDataNamespace
}

type CacheItem struct {
	Key   string
	Value any
}

func BuildGameDataCacheKey(surface, server, dataType string, userID int64, requestKey string) string {
	trimmedKey := strings.TrimSpace(requestKey)
	queryString := ""
	if trimmedKey != "" {
		queryString = "key=" + trimmedKey
	}

	var pathBuilder strings.Builder
	pathBuilder.Grow(len(surface) + len(server) + len(dataType) + 32)
	pathBuilder.WriteString(strings.TrimSpace(surface))
	pathBuilder.WriteByte(':')
	pathBuilder.WriteString(strings.TrimSpace(server))
	pathBuilder.WriteByte(':')
	pathBuilder.WriteString(strings.TrimSpace(dataType))
	pathBuilder.WriteByte(':')
	pathBuilder.WriteString(strconv.FormatInt(userID, 10))
	return buildCacheKey(gameDataNamespace, pathBuilder.String(), queryString)
}

// BuildVersionedGameDataCacheKey keys a cached body by the document generation
// that produced it (the stored upload_time). The version segment sits after
// query=, so the per-user clear pattern in ClearCache still matches every
// generation.
func BuildVersionedGameDataCacheKey(surface, server, dataType string, userID int64, requestKey string, uploadTime int64) string {
	return BuildGameDataCacheKey(surface, server, dataType, userID, requestKey) + ":v=" + strconv.FormatInt(uploadTime, 10)
}

// BuildGameDataStampMemoKey addresses the short-lived upload_time memo for one
// stored document. It lives in the game_data namespace on purpose: the
// admin-side ClearNamespace on allowlist changes wipes it together with the
// bodies.
func BuildGameDataStampMemoKey(server, dataType string, userID int64) string {
	return buildStampKey(":stamp:", server, dataType, userID)
}

// BuildGameDataStampFallbackKey addresses the long-lived last-known stamp used
// only when Mongo cannot be reached: it lets warm cache generations keep
// serving through a Mongo outage instead of failing every read after the 60s
// memo expires. It is never trusted for conditional 304 answers.
func BuildGameDataStampFallbackKey(server, dataType string, userID int64) string {
	return buildStampKey(":stamplast:", server, dataType, userID)
}

func buildStampKey(kind, server, dataType string, userID int64) string {
	var sb strings.Builder
	sb.Grow(len(gameDataNamespace) + len(kind) + len(server) + len(dataType) + 22)
	sb.WriteString(gameDataNamespace)
	sb.WriteString(kind)
	sb.WriteString(server)
	sb.WriteByte(':')
	sb.WriteString(dataType)
	sb.WriteByte(':')
	sb.WriteString(strconv.FormatInt(userID, 10))
	return sb.String()
}

func buildCacheKey(namespace, path, queryString string) string {
	var sb strings.Builder
	sb.Grow(len(namespace) + len(path) + 40) // pre-allocate: namespace + path + "query=" + hash
	sb.WriteString(namespace)
	sb.WriteByte(':')
	sb.WriteString(path)
	sb.WriteString(":query=")
	sb.WriteString(getQueryHash(queryString))
	return sb.String()
}

func getQueryHash(queryString string) string {
	if queryString == "" {
		return emptyQueryHash
	}
	hash := sha256.Sum256([]byte(queryString))
	return hex.EncodeToString(hash[:])
}

func (r *HarukiRedisManager) SetCache(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := sonic.Marshal(value)
	if err != nil {
		harukiLogger.Errorf("Failed to marshal cache value for key %s: %v", key, err)
		return err
	}
	if err := r.Redis.Set(ctx, key, data, ttl).Err(); err != nil {
		harukiLogger.Errorf("Failed to set redis cache for key %s: %v", key, err)
		return err
	}
	return nil
}

func (r *HarukiRedisManager) SetCachesAtomically(ctx context.Context, items []CacheItem, ttl time.Duration) error {
	if len(items) == 0 {
		return nil
	}
	if ttl <= 0 {
		return fmt.Errorf("ttl must be positive")
	}
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}

	payloads := make([][]byte, len(items))
	for i, item := range items {
		if item.Key == "" {
			return fmt.Errorf("cache key at index %d is empty", i)
		}
		data, err := sonic.Marshal(item.Value)
		if err != nil {
			harukiLogger.Errorf("Failed to marshal cache value for key %s: %v", item.Key, err)
			return err
		}
		payloads[i] = data
	}

	pipe := r.Redis.TxPipeline()
	for i, item := range items {
		pipe.Set(ctx, item.Key, payloads[i], ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		harukiLogger.Errorf("Failed to set redis caches atomically: %v", err)
		return err
	}
	return nil
}

func (r *HarukiRedisManager) GetCache(ctx context.Context, key string, out any) (bool, error) {
	val, err := r.Redis.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to get redis cache for key %s: %v", key, err)
		return false, err
	}
	if err := sonic.Unmarshal([]byte(val), out); err != nil {
		harukiLogger.Errorf("Failed to unmarshal cache value for key %s: %v", key, err)
		return true, err
	}
	return true, nil
}

func (r *HarukiRedisManager) DeleteCache(ctx context.Context, key string) error {
	if err := r.Redis.Del(ctx, key).Err(); err != nil {
		harukiLogger.Errorf("Failed to delete redis cache for key %s: %v", key, err)
		return err
	}
	return nil
}

func (r *HarukiRedisManager) GetRawCache(ctx context.Context, key string) (string, bool, error) {
	if r == nil || r.Redis == nil {
		return "", false, fmt.Errorf("redis client is nil")
	}
	val, err := r.Redis.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to get raw redis cache for key %s: %v", key, err)
		return "", false, err
	}
	return val, true, nil
}

func (r *HarukiRedisManager) SetRawCache(ctx context.Context, key string, value string, ttl time.Duration) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	if err := r.Redis.Set(ctx, key, value, ttl).Err(); err != nil {
		harukiLogger.Errorf("Failed to set raw redis cache for key %s: %v", key, err)
		return err
	}
	return nil
}

func (r *HarukiRedisManager) DeleteCacheIfValueMatches(ctx context.Context, key, expected string) (bool, error) {
	encodedExpected, err := sonic.Marshal(expected)
	if err != nil {
		harukiLogger.Errorf("Failed to marshal expected cache value for key %s: %v", key, err)
		return false, err
	}
	result, err := r.Redis.Eval(ctx, redisDeleteIfMatchScript, []string{key}, expected, string(encodedExpected)).Int()
	if err != nil {
		harukiLogger.Errorf("Failed to compare-and-delete redis cache for key %s: %v", key, err)
		return false, err
	}
	return result == 1, nil
}

func (r *HarukiRedisManager) IncrementWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, fmt.Errorf("ttl must be positive")
	}
	result, err := r.Redis.Eval(ctx, redisIncrementWithTTLScript, []string{key}, ttl.Milliseconds()).Int64()
	if err != nil {
		harukiLogger.Errorf("Failed to increment redis key with ttl for key %s: %v", key, err)
		return 0, err
	}
	return result, nil
}

func (r *HarukiRedisManager) ClearPublicGameDataCaches(ctx context.Context, server string, userID int64) error {
	for _, dataType := range []string{string(harukiUtils.UploadDataTypeSuite), string(harukiUtils.UploadDataTypeMysekai)} {
		if err := r.ClearCache(ctx, dataType, server, userID); err != nil {
			return err
		}
	}
	return nil
}

func (r *HarukiRedisManager) ClearUploadedGameDataCaches(ctx context.Context, dataType, server string, userID int64) error {
	if err := r.ClearCache(ctx, dataType, server, userID); err != nil {
		return err
	}
	if dataType == string(harukiUtils.UploadDataTypeMysekaiBirthdayParty) {
		return r.ClearCache(ctx, string(harukiUtils.UploadDataTypeMysekai), server, userID)
	}
	return nil
}

func (r *HarukiRedisManager) ClearNamespace(ctx context.Context, namespace string) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return fmt.Errorf("namespace is empty")
	}

	var cursor uint64
	pattern := namespace + ":*"
	for {
		keys, nextCursor, err := r.Redis.Scan(ctx, cursor, pattern, 1000).Result()
		if err != nil {
			return fmt.Errorf("clear redis namespace scan failed: %w", err)
		}
		if len(keys) > 0 {
			if err := r.Redis.Unlink(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("clear redis namespace delete failed: %w", err)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

func (r *HarukiRedisManager) ClearCache(ctx context.Context, dataType, server string, userID int64) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	// Drop the stamp keys FIRST: the memo is the freshness authority, so the
	// next read re-resolves the current generation from Mongo even if the
	// (slower, SCAN-based) body sweep below fails midway.
	if err := r.Redis.Unlink(ctx,
		BuildGameDataStampMemoKey(server, dataType, userID),
		BuildGameDataStampFallbackKey(server, dataType, userID),
	).Err(); err != nil {
		return fmt.Errorf("clear game data stamp keys failed: %w", err)
	}
	return r.clearCachePattern(ctx, fmt.Sprintf("%s:*:%s:%s:%d:query=*", gameDataNamespace, server, dataType, userID))
}

func (r *HarukiRedisManager) clearCachePattern(ctx context.Context, pattern string) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	// COUNT 1000 + UNLINK keep the full-keyspace SCAN sweep cheap enough to run
	// in the upload path: fewer round trips against a 7-day keyspace, and
	// reclamation happens off the Redis main thread.
	var cursor uint64
	for {
		keys, nextCursor, err := r.Redis.Scan(ctx, cursor, pattern, 1000).Result()
		if err != nil {
			return fmt.Errorf("clear redis cache scan failed: %w", err)
		}
		if len(keys) > 0 {
			if err := r.Redis.Unlink(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("clear redis cache delete failed: %w", err)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}
