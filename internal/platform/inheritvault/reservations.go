package inheritvault

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/inheritvaulttombstone"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
)

type reservationKey struct{}
type ownerReservation struct {
	store  *Store
	owner  string
	active atomic.Bool
}

func (s *Store) ownerReserved(ctx context.Context, userID string) bool {
	r, ok := ctx.Value(reservationKey{}).(*ownerReservation)
	return ok && r.store == s && r.owner == userID && r.active.Load()
}

func (s *Store) reserveOwner(ctx context.Context, userID string) (context.Context, func(), error) {
	if s.ownerReserved(ctx, userID) {
		return ctx, func() {}, nil
	}
	release, err := s.db.LockUserMutations(ctx, userID)
	if err != nil {
		return ctx, nil, err
	}
	r := &ownerReservation{store: s, owner: userID}
	r.active.Store(true)
	return context.WithValue(ctx, reservationKey{}, r), sync.OnceFunc(func() {
		r.active.Store(false)
		release()
	}), nil
}

// ReserveLiveOwner serializes direct game credential use with account deletion,
// vault resets and entry destruction. Keep it until the result is persisted.
func (s *Store) ReserveLiveOwner(ctx context.Context, userID string) (context.Context, func(), error) {
	if !s.Available() {
		return ctx, nil, ErrUnavailable
	}
	reserved, release, err := s.reserveOwner(ctx, userID)
	if err != nil {
		return ctx, nil, err
	}
	live, err := s.db.User.Query().Where(user.IDEQ(userID), user.BannedEQ(false)).Exist(reserved)
	if err != nil || !live {
		release()
		if err == nil {
			err = ErrNotFound
		}
		return ctx, nil, err
	}
	return reserved, release, nil
}

func (s *Store) destroyed(ctx context.Context, userID, entryID string) (bool, error) {
	return s.db.InheritVaultTombstone.Query().Where(inheritvaulttombstone.UserIDEQ(userID), inheritvaulttombstone.EntryIDEQ(entryID)).Exist(ctx)
}

func (s *Store) requireKeyVersion(ctx context.Context, userID string, version int) error {
	key, err := s.loadKeyRow(ctx, userID)
	if errors.Is(err, ErrNoKey) {
		return ErrStale
	}
	if err != nil {
		return err
	}
	if key.KeyVersion != version {
		return ErrStale
	}
	return nil
}
