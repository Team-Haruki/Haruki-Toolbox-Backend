// Package gamedata is the PostgreSQL store for Project Sekai suite/mysekai
// game data.
//
// It deliberately does NOT go through Ent. Ent unmarshals a json column into a
// Go value on read, which reconstructs exactly the BSON -> Go -> JSON round trip
// this store exists to remove; measured, that round trip is where 14.5-47x of
// the read cost lives. Values move as bytes from the column into the response
// body and are never decoded on the way out.
//
// It also uses pgx rather than the lib/pq pool Ent runs on: lib/pq speaks only
// the text protocol, so every []byte comes back hex-encoded with an `\x` prefix
// and has to be decoded per read.
package gamedata

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig configures the dedicated game-data connection pool.
type PoolConfig struct {
	// URL is the PostgreSQL DSN.
	URL string
	// MaxConns is the pool ceiling.
	MaxConns int32
	// MinConns is the number of connections established eagerly at startup.
	//
	// It defaults to MaxConns and should stay there. pgx creates connections
	// LAZILY, so without a warm pool every worker's first Acquire finds it
	// empty: measured at concurrency 4/8/16 the pool's EmptyAcquireCount came
	// out as exactly 4/8/16 — it was reporting pool construction, not
	// saturation. Warmed, the same load reports 0.
	//
	// This is also a monitoring contract. NewPool pings before warming — a bad
	// DSN must fail at boot, not on the first request — and that ping acquires a
	// connection while the pool is still empty, so EmptyAcquireCount is exactly
	// 1 after a healthy startup. Alert on the DELTA from that baseline, never on
	// > 0, or every restart looks like saturation.
	MinConns int32
	// MaxConnLifetime bounds how long one connection is reused.
	MaxConnLifetime time.Duration
	// MaxConnIdleTime reaps idle connections above MinConns.
	MaxConnIdleTime time.Duration
	// WarmupTimeout bounds how long NewPool waits for MinConns to be live.
	WarmupTimeout time.Duration
}

// Pool is the game-data connection pool.
type Pool struct {
	*pgxpool.Pool
}

// NewPool opens, warms and verifies the pool.
//
// It pings rather than trusting construction: pgxpool.New is lazy in exactly the
// same way database/sql's Open is, so a wrong DSN or an unreachable server would
// otherwise surface as a failure on the first request instead of at boot.
func NewPool(ctx context.Context, cfg PoolConfig) (*Pool, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("gamedata: empty PostgreSQL URL")
	}
	pgxCfg, err := resolvePoolConfig(cfg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return nil, fmt.Errorf("gamedata: open pool: %w", err)
	}
	p := &Pool{Pool: pool}
	if err := p.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("gamedata: ping: %w", err)
	}
	p.warm(ctx, pgxCfg.MinConns, cfg.WarmupTimeout)
	return p, nil
}

// resolvePoolConfig turns a PoolConfig into a pgx config. Split out from NewPool
// so the sizing rules can be tested without a live server.
func resolvePoolConfig(cfg PoolConfig) (*pgxpool.Config, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("gamedata: empty PostgreSQL URL")
	}
	pgxCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("gamedata: parse DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		pgxCfg.MaxConns = cfg.MaxConns
	}
	// MinConns defaults to MaxConns, and is clamped to it: a MinConns above the
	// ceiling makes pgxpool refuse to start, and a MinConns of 0 gives back the
	// lazy pool whose first-Acquire cost looks exactly like saturation.
	pgxCfg.MinConns = cfg.MinConns
	if pgxCfg.MinConns <= 0 || pgxCfg.MinConns > pgxCfg.MaxConns {
		pgxCfg.MinConns = pgxCfg.MaxConns
	}
	if cfg.MaxConnLifetime > 0 {
		pgxCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		pgxCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	return pgxCfg, nil
}

// warm waits for MinConns idle connections so the first burst of real traffic
// does not pay connection setup. Best effort: a slow database delays startup at
// most WarmupTimeout, it never fails it — Ping already proved reachability.
func (p *Pool) warm(ctx context.Context, minConns int32, timeout time.Duration) {
	if minConns <= 0 {
		return
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := p.Stat()
		if st.TotalConns() >= minConns && st.IdleConns() >= minConns {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Close releases the pool.
func (p *Pool) Close() error {
	if p == nil || p.Pool == nil {
		return nil
	}
	p.Pool.Close()
	return nil
}
