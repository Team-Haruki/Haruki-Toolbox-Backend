package runtimeconfig

import (
	"context"
	"errors"
	"fmt"

	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/bytedance/sonic"
	"github.com/redis/go-redis/v9"
)

const maxRedisApplyAttempts = 32

type redisStore struct {
	manager *harukiRedis.HarukiRedisManager
}

// NewRedisStore adapts the existing Redis manager without changing the stored
// key or JSON representation. A nil manager means runtime settings are local to
// this process.
func NewRedisStore(manager *harukiRedis.HarukiRedisManager) Store {
	if manager == nil || manager.Redis == nil {
		return nil
	}
	return &redisStore{manager: manager}
}

func (s *redisStore) Load(ctx context.Context) (Snapshot, bool, error) {
	var snapshot Snapshot
	found, err := s.manager.GetCache(ctx, harukiRedis.BuildRuntimeConfigKey(), &snapshot)
	return snapshot, found, err
}

func (s *redisStore) Apply(ctx context.Context, update Update, fallback Snapshot) (Snapshot, error) {
	key := harukiRedis.BuildRuntimeConfigKey()
	for attempt := 1; attempt <= maxRedisApplyAttempts; attempt++ {
		var committed Snapshot
		err := s.manager.Redis.Watch(ctx, func(tx *redis.Tx) error {
			current := cloneSnapshot(fallback)
			payload, err := tx.Get(ctx, key).Bytes()
			switch {
			case errors.Is(err, redis.Nil):
			case err != nil:
				return err
			default:
				if err := sonic.Unmarshal(payload, &current); err != nil {
					return err
				}
			}

			applyUpdate(&current, update)
			encoded, err := sonic.Marshal(current)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, encoded, 0)
				return nil
			})
			if err == nil {
				committed = cloneSnapshot(current)
			}
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Snapshot{}, ctxErr
			}
			continue
		}
		if err != nil {
			return Snapshot{}, err
		}
		return committed, nil
	}
	return Snapshot{}, fmt.Errorf("runtime config transaction conflicted after %d attempts", maxRedisApplyAttempts)
}
