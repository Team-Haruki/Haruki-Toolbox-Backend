package api

import (
	"context"
	"fmt"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/redis/go-redis/v9"
)

func ClearUserSessions(redisClient *redis.Client, userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), clearUserSessionsTimeout)
	defer cancel()
	return ClearUserSessionsWithContext(ctx, redisClient, userID)
}

func ClearUserSessionsWithContext(ctx context.Context, redisClient *redis.Client, userID string) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is nil")
	}
	var cursor uint64
	prefix := userID + ":"
	for {
		keys, newCursor, err := redisClient.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			harukiLogger.Errorf("Redis scan error: %v", err)
			return err
		}
		if len(keys) > 0 {
			if err := redisClient.Del(ctx, keys...).Err(); err != nil {
				harukiLogger.Errorf("Redis del error: %v", err)
				return err
			}
		}
		cursor = newCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}
