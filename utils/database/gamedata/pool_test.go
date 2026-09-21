package gamedata

import (
	"testing"
	"time"
)

const testDSN = "postgres://u:p@127.0.0.1:5432/db?sslmode=disable"

// The pool must always start warm. An unwarmed pgx pool reports an
// EmptyAcquireCount equal to the worker count on the first burst, which reads
// as saturation and is not.
func TestMinConnsDefaultsToMaxConns(t *testing.T) {
	cfg, err := resolvePoolConfig(PoolConfig{URL: testDSN, MaxConns: 12})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinConns != cfg.MaxConns {
		t.Fatalf("MinConns = %d, want MaxConns = %d", cfg.MinConns, cfg.MaxConns)
	}
	if cfg.MaxConns != 12 {
		t.Fatalf("MaxConns = %d, want 12", cfg.MaxConns)
	}
}

// A MinConns above the ceiling makes pgxpool refuse to start; clamp instead of
// handing the operator a startup failure for a merely odd number.
func TestMinConnsIsClampedToMaxConns(t *testing.T) {
	cfg, err := resolvePoolConfig(PoolConfig{URL: testDSN, MaxConns: 4, MinConns: 99})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinConns != 4 {
		t.Fatalf("MinConns = %d, want clamp to 4", cfg.MinConns)
	}
}

func TestMinConnsHonouredWhenBelowCeiling(t *testing.T) {
	cfg, err := resolvePoolConfig(PoolConfig{URL: testDSN, MaxConns: 10, MinConns: 3})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinConns != 3 {
		t.Fatalf("MinConns = %d, want 3", cfg.MinConns)
	}
}

// MaxConns 0 must leave pgx's own default rather than becoming a zero ceiling.
func TestZeroMaxConnsKeepsThePgxDefault(t *testing.T) {
	cfg, err := resolvePoolConfig(PoolConfig{URL: testDSN})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns <= 0 {
		t.Fatalf("MaxConns = %d, want pgx's positive default", cfg.MaxConns)
	}
	if cfg.MinConns != cfg.MaxConns {
		t.Fatalf("MinConns = %d, want MaxConns = %d", cfg.MinConns, cfg.MaxConns)
	}
}

func TestLifetimesArePassedThrough(t *testing.T) {
	cfg, err := resolvePoolConfig(PoolConfig{
		URL: testDSN, MaxConns: 4,
		MaxConnLifetime: 90 * time.Second,
		MaxConnIdleTime: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConnLifetime != 90*time.Second || cfg.MaxConnIdleTime != 30*time.Second {
		t.Fatalf("lifetimes not passed through: %v / %v", cfg.MaxConnLifetime, cfg.MaxConnIdleTime)
	}
}

func TestEmptyURLIsRejected(t *testing.T) {
	if _, err := resolvePoolConfig(PoolConfig{}); err == nil {
		t.Fatal("empty URL accepted")
	}
}

func TestBadDSNIsRejected(t *testing.T) {
	if _, err := resolvePoolConfig(PoolConfig{URL: "://nonsense"}); err == nil {
		t.Fatal("malformed DSN accepted")
	}
}

// Close on a zero Pool must not panic: bootstrap registers the closer at
// acquisition and the unwind runs even on a partially built process.
func TestCloseOnNilPoolIsSafe(t *testing.T) {
	var p *Pool
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&Pool{}).Close(); err != nil {
		t.Fatal(err)
	}
}
