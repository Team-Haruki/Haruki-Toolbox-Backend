package bootstrap

import (
	"context"
	"fmt"
	harukiRedis "haruki-suite/utils/database/redis"
	"time"
)

const startupDependencyTimeout = 15 * time.Second

func ensureRedisReady(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager) error {
	if redisManager == nil || redisManager.Redis == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	if err := redisManager.Redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	return nil
}

func startupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), startupDependencyTimeout)
}
