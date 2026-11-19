package postgresql

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/pgadvisory"
)

// Leave connections for the operations that run while a reservation is held.
// The production Toolbox pool has 50 slots.
var databaseMutationAdmission = make(chan struct{}, 24)

// LockUserMutations serializes actions that rely on a user's current role or
// vault identity. The release must cover external changes as well as DB writes.
func (c *Client) LockUserMutations(ctx context.Context, ids ...string) (func(), error) {
	return c.lockMutations(ctx, appendUserMutationKeys(make([]string, 0, len(ids)), ids))
}

func appendUserMutationKeys(keys []string, ids []string) []string {
	for _, id := range ids {
		if id != "" {
			keys = append(keys, "user:"+id)
		}
	}
	return keys
}

// LockGameAccountMutation serializes binding/grant changes with commits to the
// separate game-data database.
func (c *Client) LockGameAccountMutation(ctx context.Context, server, gameUserID string) (func(), error) {
	return c.LockGameAccountAndUserMutations(ctx, server, gameUserID)
}

// LockGameAccountAndUserMutations reserves the game and its associated users
// in game-then-user order, using one admission slot and one transaction.
func (c *Client) LockGameAccountAndUserMutations(ctx context.Context, server, gameUserID string, ids ...string) (func(), error) {
	keys := make([]string, 1, 1+len(ids))
	keys[0] = "game:" + server + ":" + gameUserID
	return c.lockMutations(ctx, appendUserMutationKeys(keys, ids))
}

func (c *Client) lockMutations(ctx context.Context, keys []string) (func(), error) {
	if c == nil || c.driver == nil {
		return nil, errors.New("mutation lock requires the database")
	}
	if len(keys) == 0 {
		return func() {}, nil
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if c.config.driver.Dialect() == dialect.SQLite {
		return lockLocalMutations(wait, keys)
	}
	db := c.SQLDB()
	if db == nil {
		return nil, errors.New("mutation lock requires a SQL connection")
	}
	select {
	case databaseMutationAdmission <- struct{}{}:
	case <-wait.Done():
		return nil, wait.Err()
	}
	unlock, err := pgadvisory.Lock(wait, db, `SELECT pg_advisory_xact_lock(hashtextextended($1, 452731))`, keys...)
	if err != nil {
		<-databaseMutationAdmission
		return nil, err
	}
	return sync.OnceFunc(func() {
		unlock()
		<-databaseMutationAdmission
	}), nil
}

type localMutationGate struct {
	slot chan struct{}
	refs int
}

var localMutations = struct {
	sync.Mutex
	gates map[string]*localMutationGate
}{gates: make(map[string]*localMutationGate)}

func lockLocalMutations(ctx context.Context, keys []string) (func(), error) {
	var releases []func()
	release := sync.OnceFunc(func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	})
	for _, key := range keys {
		localMutations.Lock()
		gate := localMutations.gates[key]
		if gate == nil {
			gate = &localMutationGate{slot: make(chan struct{}, 1)}
			localMutations.gates[key] = gate
		}
		gate.refs++
		localMutations.Unlock()
		unref := func() {
			localMutations.Lock()
			defer localMutations.Unlock()
			gate.refs--
			if gate.refs == 0 {
				delete(localMutations.gates, key)
			}
		}
		select {
		case gate.slot <- struct{}{}:
			releases = append(releases, func() { <-gate.slot; unref() })
		case <-ctx.Done():
			unref()
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}
