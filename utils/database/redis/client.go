package redis

import (
	"context"
	"fmt"
	"haruki-suite/config"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/redis/go-redis/v9"
)

type HarukiRedisManager struct {
	Redis *redis.Client
}

func NewRedisClient(cfg config.RedisConfig) *HarukiRedisManager {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       0,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		harukiLogger.Errorf("Failed to connect to Redis: %v", err)
	}
	return &HarukiRedisManager{
		Redis: client,
	}
}
