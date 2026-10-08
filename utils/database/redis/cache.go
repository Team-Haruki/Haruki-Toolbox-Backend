package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"

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
	// the game-data database. It is the staleness ceiling for every
	// stamp-changing write path (uploads, backfill, binding clears); the
	// residual same-second upload window is fenced separately at cache-write
	// time (see ConfirmGameDataCacheWrite).
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

// gameDataBodyVersionTag prefixes the generation segment of a versioned body
// key. "v2" marks bodies written together with their per-document index entry
// (see SetGameDataBodyCache); the "v=" bodies of earlier releases were never
// indexed, so ClearCache could not reach them, and readers no longer look them
// up. They expire by TTL or volatile-lru.
const gameDataBodyVersionTag = ":v2="

// BuildVersionedGameDataCacheKey keys a cached body by the document generation
// that produced it (the stored upload_time). Bodies must be written with
// SetGameDataBodyCache so ClearCache can find every generation of a document.
func BuildVersionedGameDataCacheKey(surface, server, dataType string, userID int64, requestKey string, uploadTime int64, harvestFingerprint ...string) string {
	profile := ""
	if len(harvestFingerprint) > 0 {
		profile = harvestFingerprint[0]
	}
	suffix := ""
	if profile != "" {
		suffix = ":harvest=" + profile + "-1"
	}
	return BuildGameDataCacheKey(surface, server, dataType, userID, requestKey) + gameDataBodyVersionTag + strconv.FormatInt(uploadTime, 10) + suffix
}

// isGameDataBodyKey reports whether key is a cached game-data body (any
// surface), as opposed to a stamp or index key in the same namespace.
func isGameDataBodyKey(key string) bool {
	return strings.HasPrefix(key, gameDataNamespace+":") && strings.Contains(key, ":query=")
}

// BuildGameDataBodyIndexKey addresses the set of every body key cached for one
// stored document, across surfaces, ?key= filters and generations. It lives in
// the game_data namespace so the allowlist ClearNamespace wipes it with the
// bodies.
func BuildGameDataBodyIndexKey(server, dataType string, userID int64) string {
	return buildStampKey(":idx:", server, dataType, userID)
}

// BuildGameDataStampMemoKey addresses the short-lived upload_time memo for one
// stored document. It lives in the game_data namespace on purpose: the
// admin-side ClearNamespace on allowlist changes wipes it together with the
// bodies.
func BuildGameDataStampMemoKey(server, dataType string, userID int64) string {
	return buildStampKey(":stamp:", server, dataType, userID)
}

// BuildGameDataStampFallbackKey addresses the long-lived last-known stamp used
// only when the game-data database cannot be reached: it lets warm cache
// generations keep serving through a database outage instead of failing every
// read after the 60s memo expires. It is never trusted for conditional 304 answers.
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
	data, err := jsoncodec.Marshal(value)
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
		data, err := jsoncodec.Marshal(item.Value)
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
	if err := jsoncodec.Unmarshal([]byte(val), out); err != nil {
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

// GetRawCacheBytes is GetRawCache for callers that send the value straight to
// a response: go-redis hands back its string's bytes without another copy. The
// slice must be treated as read-only.
func (r *HarukiRedisManager) GetRawCacheBytes(ctx context.Context, key string) ([]byte, bool, error) {
	if r == nil || r.Redis == nil {
		return nil, false, fmt.Errorf("redis client is nil")
	}
	val, err := r.Redis.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to get raw redis cache for key %s: %v", key, err)
		return nil, false, err
	}
	return val, true, nil
}

func (r *HarukiRedisManager) SetRawCacheBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	if err := r.Redis.Set(ctx, key, value, ttl).Err(); err != nil {
		harukiLogger.Errorf("Failed to set raw redis cache for key %s: %v", key, err)
		return err
	}
	return nil
}

func (r *HarukiRedisManager) SetRawCache(ctx context.Context, key string, value string, ttl time.Duration) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	if isGameDataBodyKey(key) {
		// An unindexed body would survive ClearCache until its TTL.
		return fmt.Errorf("game data body key %s must be written with SetGameDataBodyCache", key)
	}
	if err := r.Redis.Set(ctx, key, value, ttl).Err(); err != nil {
		harukiLogger.Errorf("Failed to set raw redis cache for key %s: %v", key, err)
		return err
	}
	return nil
}

// SetGameDataBodyCache stores a game-data response body under key and records
// key in the document's index set in the same transaction, so ClearCache can
// drop every cached body of the document without scanning the keyspace. The
// index lives at least as long as the longest body TTL; members whose bodies
// already expired are harmless and go with the next clear.
func (r *HarukiRedisManager) SetGameDataBodyCache(ctx context.Context, server, dataType string, userID int64, key, body string, ttl time.Duration) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	indexTTL := max(ttl, GameDataCacheTTL)
	indexKey := BuildGameDataBodyIndexKey(server, dataType, userID)
	pipe := r.Redis.TxPipeline()
	pipe.Set(ctx, key, body, ttl)
	pipe.SAdd(ctx, indexKey, key)
	pipe.Expire(ctx, indexKey, indexTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		harukiLogger.Errorf("Failed to set game data body cache for key %s: %v", key, err)
		return err
	}
	return nil
}

func (r *HarukiRedisManager) DeleteCacheIfValueMatches(ctx context.Context, key, expected string) (bool, error) {
	encodedExpected, err := jsoncodec.Marshal(expected)
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

// gameDataClearBatch caps the keys per UNLINK when clearing a document.
const gameDataClearBatch = 500

// ClearCache drops one stored document's stamp keys and every cached body of it.
// The stamp keys go first, in the same transaction that takes and resets the
// body index: the memo is the freshness authority, so the next read re-resolves
// the current generation from the database even if the body UNLINKs below fail.
// A body written after the transaction starts a fresh index, so it is cleared by
// the next ClearCache, not lost.
func (r *HarukiRedisManager) ClearCache(ctx context.Context, dataType, server string, userID int64) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	indexKey := BuildGameDataBodyIndexKey(server, dataType, userID)
	var members *redis.StringSliceCmd
	if _, err := r.Redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Unlink(ctx,
			BuildGameDataStampMemoKey(server, dataType, userID),
			BuildGameDataStampFallbackKey(server, dataType, userID),
		)
		members = pipe.SMembers(ctx, indexKey)
		pipe.Unlink(ctx, indexKey)
		return nil
	}); err != nil {
		return fmt.Errorf("clear game data stamp and index keys failed: %w", err)
	}
	keys := members.Val()
	for len(keys) > 0 {
		batch := keys[:min(len(keys), gameDataClearBatch)]
		keys = keys[len(batch):]
		if err := r.Redis.Unlink(ctx, batch...).Err(); err != nil {
			return fmt.Errorf("clear game data body keys failed: %w", err)
		}
	}
	return nil
}
