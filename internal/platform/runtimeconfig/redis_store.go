package runtimeconfig

import (
	"context"

	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"math"

	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/redis/go-redis/v9"
)

const maxRedisApplyAttempts = 32

type redisStore struct {
	manager *harukiRedis.HarukiRedisManager
}

// NewRedisStore adapts the legacy Redis key and accepts unversioned snapshots.
// A nil manager means runtime settings are local to this process.
func NewRedisStore(manager *harukiRedis.HarukiRedisManager) Store {
	if manager == nil || manager.Redis == nil {
		return nil
	}
	return &redisStore{manager: manager}
}

func (s *redisStore) Load(ctx context.Context) (Snapshot, bool, error) {
	var snapshot Snapshot
	var versionCmd, snapshotCmd *redis.StringCmd
	_, err := s.manager.Redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		versionCmd = pipe.Get(ctx, harukiRedis.BuildRuntimeConfigVersionKey())
		snapshotCmd = pipe.Get(ctx, harukiRedis.BuildRuntimeConfigKey())
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return snapshot, false, err
	}
	version, err := redisRuntimeConfigVersion(versionCmd)
	if err != nil {
		return snapshot, false, err
	}
	payload, err := snapshotCmd.Bytes()
	if errors.Is(err, redis.Nil) {
		return Snapshot{Version: version}, false, nil
	}
	if err != nil {
		return snapshot, false, err
	}
	if err := unmarshalSnapshot(payload, &snapshot); err != nil {
		return snapshot, false, err
	}
	if snapshot.Version < version {
		return Snapshot{}, false, errRuntimeConfigVersionRegressed
	}
	return snapshot, true, nil
}

func (s *redisStore) Apply(ctx context.Context, update Update, fallback Snapshot) (Snapshot, error) {
	key := harukiRedis.BuildRuntimeConfigKey()
	versionKey := harukiRedis.BuildRuntimeConfigVersionKey()
	for attempt := 1; attempt <= maxRedisApplyAttempts; attempt++ {
		var committed Snapshot
		err := s.manager.Redis.Watch(ctx, func(tx *redis.Tx) error {
			version, err := redisRuntimeConfigVersion(tx.Get(ctx, versionKey))
			if err != nil {
				return err
			}
			var current Snapshot
			payload, err := tx.Get(ctx, key).Bytes()
			switch {
			case errors.Is(err, redis.Nil):
				// Seed startup settings only before any distributed snapshot
				// has existed. A lost snapshot cannot restore stale credentials.
				if version == 0 && fallback.Version == 0 {
					current = cloneSnapshot(fallback)
				}
			case err != nil:
				return err
			default:
				if err := unmarshalSnapshot(payload, &current); err != nil {
					return err
				}
				if current.Version < version {
					current = Snapshot{}
				}
			}

			version = max(version, current.Version)
			if version == math.MaxUint64 {
				return errors.New("runtime configuration version is exhausted")
			}
			applyUpdate(&current, update)
			current.Version = version + 1
			encoded, err := marshalSnapshot(current)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, encoded, 0)
				pipe.Set(ctx, versionKey, current.Version, 0)
				return nil
			})
			if err == nil {
				committed = cloneSnapshot(current)
			}
			return err
		}, key, versionKey)
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

func redisRuntimeConfigVersion(cmd *redis.StringCmd) (uint64, error) {
	version, err := cmd.Uint64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return version, err
}

// Preserve the existing nil allowlist and native Sonic escaping on writes.
// Reads use v2 strict syntax and exact member names.
func marshalSnapshot(snapshot Snapshot) ([]byte, error) {
	return jsonv2.Marshal(snapshot, jsonv2.FormatNilSliceAsNull(true), jsontext.EscapeForHTML(false), jsontext.EscapeForJS(false))
}

func unmarshalSnapshot(payload []byte, snapshot *Snapshot) error {
	return jsonv2.Unmarshal(payload, snapshot)
}
