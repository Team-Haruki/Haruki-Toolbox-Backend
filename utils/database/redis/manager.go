package redis

import (
	"fmt"
	"haruki-suite/config"

	"github.com/redis/go-redis/v9"
)

func NewRedisClient(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       0,
	})
}
