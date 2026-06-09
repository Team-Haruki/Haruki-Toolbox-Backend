package redis

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
)

const (
	gameDataNamespace = "game_data"
	emptyQueryHash    = "none"

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
	hash := md5.Sum([]byte(queryString))
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
		keys, nextCursor, err := r.Redis.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("clear redis namespace scan failed: %w", err)
		}
		if len(keys) > 0 {
			if err := r.Redis.Del(ctx, keys...).Err(); err != nil {
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
	if err := r.clearCachePattern(ctx, fmt.Sprintf("%s:*:%s:%s:%d:query=*", gameDataNamespace, server, dataType, userID)); err != nil {
		return err
	}
	return nil
}

func (r *HarukiRedisManager) clearCachePattern(ctx context.Context, pattern string) error {
	if r == nil || r.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	var cursor uint64
	for {
		keys, nextCursor, err := r.Redis.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("clear redis cache scan failed: %w", err)
		}
		if len(keys) > 0 {
			if err := r.Redis.Del(ctx, keys...).Err(); err != nil {
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
