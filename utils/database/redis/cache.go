package redis

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	harukiLogger "haruki-suite/utils/logger"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

const (
	publicAccessNamespace = "public_access"
	emptyQueryHash        = "none"
	cacheKeyFormat        = "%s:%s:query=%s"

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

type CachePath struct {
	Namespace   string
	Path        string
	QueryString string
}

type CacheItem struct {
	Key   string
	Value any
}

func GetClearCachePaths(server string, dataType string, userID int64) []CachePath {
	return []CachePath{
		{
			Namespace: publicAccessNamespace,
			Path:      fmt.Sprintf("/public/%s/%s/%d", server, dataType, userID),
		},
	}
}

func CacheKeyBuilder(c fiber.Ctx, namespace string) string {
	fullPath := c.Path()
	queryString := c.RequestCtx().QueryArgs().String()
	return buildCacheKey(namespace, fullPath, queryString)
}

func CacheKeyBuilderWithAllowedQuery(c fiber.Ctx, namespace string, allowedQueryKeys ...string) string {
	fullPath := c.Path()
	if len(allowedQueryKeys) == 0 {
		return buildCacheKey(namespace, fullPath, "")
	}

	keys := append([]string(nil), allowedQueryKeys...)
	sort.Strings(keys)
	values := url.Values{}
	for _, key := range keys {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			continue
		}
		normalizedValue := strings.TrimSpace(c.Query(normalizedKey))
		if normalizedValue == "" {
			continue
		}
		values.Set(normalizedKey, normalizedValue)
	}
	return buildCacheKey(namespace, fullPath, values.Encode())
}

func buildCacheKey(namespace, path, queryString string) string {
	return fmt.Sprintf(cacheKeyFormat, namespace, path, getQueryHash(queryString))
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

func (r *HarukiRedisManager) ClearCache(ctx context.Context, dataType, server string, userID int64) error {
	paths := GetClearCachePaths(server, dataType, userID)
	for _, path := range paths {
		if path.Namespace == "" || path.Path == "" {
			continue
		}
		pattern := fmt.Sprintf("%s:%s:query=*", path.Namespace, path.Path)
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
	}
	return nil
}
