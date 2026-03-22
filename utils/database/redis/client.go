package redis

import (
	"context"
	"fmt"
	"haruki-suite/config"
	harukiLogger "haruki-suite/utils/logger"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisPingTimeout = 5 * time.Second

type HarukiRedisManager struct {
	Redis *redis.Client
}

func (r *HarukiRedisManager) Close() error {
	if r == nil || r.Redis == nil {
		return nil
	}
	return r.Redis.Close()
}

func NewRedisClient(cfg config.RedisConfig) *HarukiRedisManager {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), redisPingTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		harukiLogger.Errorf("Failed to connect to Redis: %v", err)
	}
	return &HarukiRedisManager{
		Redis: client,
	}
}
