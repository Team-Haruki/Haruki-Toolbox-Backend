package redis

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func ClearCache(ctx context.Context, redisClient *redis.Client, dataType, server string, userID int64) error {
	paths := GetClearCachePaths(dataType, server, userID)
	for _, path := range paths {
		sum := md5.Sum([]byte(path.QueryString))
		queryHash := hex.EncodeToString(sum[:])
		if err := DeleteCache(ctx, redisClient, fmt.Sprintf("%s:%s:query=%s", path.Namespace, path.Path, queryHash)); err != nil {
			return errors.New(fmt.Sprintf("clear redis cache failed: %v", err))
		}
	}
	return nil
}
