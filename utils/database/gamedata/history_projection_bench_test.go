package gamedata

import (
	"context"
	"os"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/gamemerge"
)

// The DSN must point to a disposable benchmark database: this recreates game_suite.
// Both cases use the same row and transaction; AllHistories models the previous
// fixed projection for a partial events upload. This is not an upload benchmark.
func BenchmarkHistoryProjection(b *testing.B) {
	dsn := os.Getenv("GAMEDATA_HISTORY_BENCH_PG")
	if dsn == "" {
		b.Skip("set GAMEDATA_HISTORY_BENCH_PG to a disposable database")
	}
	ctx := context.Background()
	pool, err := NewPool(ctx, PoolConfig{URL: dsn, MaxConns: 2})
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS "game_suite"`); err != nil {
		b.Fatal(err)
	}
	if _, err := EnsureSchema(ctx, pool, true, catalog.Suite()); err != nil {
		b.Fatal(err)
	}
	s := NewStore(pool, catalog.Suite())
	code, _ := catalog.ServerCode("cn")
	const id = int64(28808221489823746)
	fixture := map[string]any{}
	for _, key := range gamemerge.Keys() {
		n := 10000
		if key == gamemerge.KeyUserEvents {
			n = 180
		}
		rows := make([]any, n)
		for i := range rows {
			rows[i] = map[string]any{"eventId": i + 1, "eventPoint": i * 12345, "gachaId": i + 1, "lastSpinAt": 1750000000000 + i}
		}
		fixture[key] = rows
	}
	if _, err := s.Write(ctx, id, "cn", fixture, WriteMysekai, DefaultLimits()); err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		keys map[string]any
	}{
		{"AllHistories", fixture},
		{"EventsOnly", map[string]any{gamemerge.KeyUserEvents: nil}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tx, err := pool.Begin(ctx)
				if err != nil {
					b.Fatal(err)
				}
				values, err := s.readMergedColumns(ctx, tx, id, code, tc.keys)
				if err != nil {
					_ = tx.Rollback(ctx)
					b.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					b.Fatal(err)
				}
				if len(values) != len(tc.keys) {
					b.Fatal("benchmark did not read the seeded histories")
				}
			}
		})
	}
}
