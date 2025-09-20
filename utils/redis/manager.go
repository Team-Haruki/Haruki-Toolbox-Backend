package redis

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"haruki-suite/config"

	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func NewRedisClient(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       0,
	})
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
