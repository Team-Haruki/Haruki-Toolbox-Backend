package redis

import (
	"fmt"
	"haruki-suite/config"

	"github.com/redis/go-redis/v9"
)

type HarukiRedisManager struct {
	Redis *redis.Client
}

func NewRedisClient(cfg config.RedisConfig) *HarukiRedisManager {
	return &HarukiRedisManager{
		Redis: redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Password: cfg.Password,
			DB:       0,
		}),
	}
}
