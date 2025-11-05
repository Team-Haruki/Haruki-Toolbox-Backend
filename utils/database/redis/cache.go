package redis

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

type CachePath struct {
	Namespace   string
	Path        string
	QueryString string
}

func GetClearCachePaths(server string, dataType string, userID int64) []CachePath {
	return []CachePath{
		{
			Namespace: "public_access",
			Path:      fmt.Sprintf("/public/%s/%s/%d", server, dataType, userID),
		},
		{
			Namespace:   "public_access",
			Path:        fmt.Sprintf("/public/%s/%s/%d", server, dataType, userID),
			QueryString: "key=upload_time",
		},
	}
}

func CacheKeyBuilder(c fiber.Ctx, namespace string) string {
	fullPath := c.Path()
	queryString := c.RequestCtx().QueryArgs().String()

	queryHash := "none"
	if queryString != "" {
		hash := md5.Sum([]byte(queryString))
		queryHash = hex.EncodeToString(hash[:])
	}

	return fmt.Sprintf("%s:%s:query=%s", namespace, fullPath, queryHash)
}

func (r *HarukiRedisManager) SetCache(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := sonic.Marshal(value)
	if err != nil {
		return err
	}
	return r.Redis.Set(ctx, key, data, ttl).Err()
}

func (r *HarukiRedisManager) GetCache(ctx context.Context, key string, out interface{}) (bool, error) {
	val, err := r.Redis.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, sonic.Unmarshal([]byte(val), out)
}

func (r *HarukiRedisManager) DeleteCache(ctx context.Context, key string) error {
	return r.Redis.Del(ctx, key).Err()
}

func (r *HarukiRedisManager) ClearCache(ctx context.Context, dataType, server string, userID int64) error {
	paths := GetClearCachePaths(server, dataType, userID)
	for _, path := range paths {
		queryHash := "none"
		if path.QueryString != "" {
			sum := md5.Sum([]byte(path.QueryString))
			queryHash = hex.EncodeToString(sum[:])
		}
		if err := r.DeleteCache(ctx, fmt.Sprintf("%s:%s:query=%s", path.Namespace, path.Path, queryHash)); err != nil {
			return errors.New(fmt.Sprintf("clear redis cache failed: %v", err))
		}
	}
	return nil
}
