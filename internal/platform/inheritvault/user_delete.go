package inheritvault

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// DeleteUserFallback removes sealed records awaiting a database retry. The
// caller holds the transfer-job admission lock; live vault rows are removed
// by the users foreign key in the account deletion transaction.
func DeleteUserFallback(ctx context.Context, rdb *redis.Client, userID string) error {
	if rdb == nil {
		return errors.New("inherit vault: redis unavailable")
	}
	s := &Store{redis: rdb}
	ids, err := rdb.SMembers(ctx, fallbackUserPrefix+userID).Result()
	if err != nil {
		return err
	}
	for _, id := range ids {
		rec, err := s.getFallback(ctx, id)
		if err != nil {
			return err
		}
		if rec != nil && rec.UserID != userID {
			return errors.New("inherit vault: fallback owner mismatch")
		}
		if err := s.dropFallback(ctx, id, userID); err != nil {
			return err
		}
	}
	return rdb.Del(ctx, fallbackUserPrefix+userID).Err()
}
