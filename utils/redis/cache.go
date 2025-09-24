package redis

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
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

func CacheKeyBuilder(c *fiber.Ctx, namespace string) string {
	fullPath := c.Path() // 请求路径
	queryString := c.Context().QueryArgs().String()

	queryHash := "none"
	if queryString != "" {
		hash := md5.Sum([]byte(queryString))
		queryHash = hex.EncodeToString(hash[:])
	}

	return fmt.Sprintf("%s:%s:query=%s", namespace, fullPath, queryHash)
}

func SetCache(ctx context.Context, client *redis.Client, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return client.Set(ctx, key, data, ttl).Err()
}

func GetCache(ctx context.Context, client *redis.Client, key string, out interface{}) (bool, error) {
	val, err := client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(val), out)
}

func DeleteCache(ctx context.Context, client *redis.Client, key string) error {
	return client.Del(ctx, key).Err()
}
