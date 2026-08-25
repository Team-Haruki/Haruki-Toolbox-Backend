package gamedata

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// The cutover's M5/M6 gates, measured against the SHIPPING store and a table the
// shipping CLI loaded — not against the throwaway rehearsal harness.
//
//	M5  single-row upsert p99 < 200 ms on the largest production rows
//	M6  pool EmptyAcquireCount stays 0 at twice peak concurrency
//
// M6 is the one that is easy to misread: an unwarmed pgx pool reports a count
// equal to the worker number on the first burst, which looks exactly like
// saturation. The pool warms itself, so a non-zero result here is real.
//
//	GAMEDATA_GATE_PG=postgres://...   (a database already loaded by gamedata-migrate)
type target struct {
	id     int64
	server string
}

func TestCutoverGatesM5AndM6(t *testing.T) {
	dsn := os.Getenv("GAMEDATA_GATE_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_GATE_PG to measure the M5/M6 cutover gates")
	}
	ctx := context.Background()
	const maxConns = 20

	pool, err := NewPool(ctx, PoolConfig{URL: dsn, MaxConns: maxConns})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	// Baseline, not zero: NewPool pings before warming so a bad DSN fails at
	// boot, and that ping acquires a connection while the pool is still empty.
	// A healthy startup therefore lands on exactly 1. The gate measures the
	// DELTA from here — asserting > 0 would fail every restart.
	baseline := pool.Stat().EmptyAcquireCount()
	if baseline > 1 {
		t.Fatalf("the pool reported %d empty acquires during warm-up; it did not warm", baseline)
	}
	warm := pool.Stat()
	if warm.IdleConns() < maxConns-1 {
		t.Fatalf("pool did not warm: idle=%d of %d", warm.IdleConns(), maxConns)
	}
	t.Logf("pool warmed: total=%d idle=%d, warm-up EmptyAcquireCount baseline=%d",
		warm.TotalConns(), warm.IdleConns(), baseline)

	store := NewStore(pool, catalog.Suite())

	// The heaviest rows in the table, which is what the gate is about.
	rows, err := pool.Query(ctx, fmt.Sprintf(
		`SELECT %s, %s FROM %s ORDER BY pg_column_size(%s.*) DESC LIMIT 40`,
		catalog.QuoteIdent(catalog.ColUserID), catalog.QuoteIdent(catalog.ColServer),
		catalog.QuoteIdent(catalog.Suite().Table), catalog.QuoteIdent(catalog.Suite().Table)))
	if err != nil {
		t.Fatal(err)
	}
	var targets []target
	for rows.Next() {
		var id int64
		var code int16
		if err := rows.Scan(&id, &code); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		name, ok := catalog.ServerName(code)
		if !ok {
			continue
		}
		targets = append(targets, target{id: id, server: name})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(targets) == 0 {
		t.Skip("the table is empty; load it with gamedata-migrate first")
	}

	const workers = 16 // twice the expected peak
	const duration = 12 * time.Second

	// Three shapes, because they cost wildly different amounts and only one of
	// them is what M5 gates on:
	//
	//   upsert       — M5's actual definition: one row written
	//   allowlist    — what the public face really does: 25 keys, not 246
	//   whole row    — the worst case, and what the private no-key face does
	//
	// Gating on the whole-row read would be gating on a shape almost no request
	// produces; not measuring it would hide the real tail.
	allowlist := []string{
		"userDecks", "userCards", "userAreas", "userHonors", "userMusics",
		"userEvents", "upload_time", "userProfile", "userCharacters",
		"userWorldBlooms", "userBondsHonors", "userMusicResults",
		"userMysekaiGates", "userProfileHonors", "userMysekaiCanvases",
		"userRankMatchSeasons", "userMysekaiMaterials", "userMusicAchievements",
		"userMysekaiCharacterTalks", "userWorldBloomSupportDecks",
		"userChallengeLiveSoloDecks", "userChallengeLiveSoloStages",
		"userChallengeLiveSoloResults", "userChallengeLiveSoloHighScoreRewards",
		"userMysekaiFixtureGameCharacterPerformanceBonuses",
	}

	run := func(name string, op func(tg target, i int) error) []time.Duration {
		var mu sync.Mutex
		var samples []time.Duration
		var failures atomic.Int64
		deadline := time.Now().Add(duration)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				local := make([]time.Duration, 0, 512)
				for i := 0; time.Now().Before(deadline); i++ {
					tg := targets[(w+i)%len(targets)]
					start := time.Now()
					if err := op(tg, w*100000+i); err != nil {
						failures.Add(1)
						continue
					}
					local = append(local, time.Since(start))
				}
				mu.Lock()
				samples = append(samples, local...)
				mu.Unlock()
			}(w)
		}
		wg.Wait()
		if failures.Load() > 0 {
			t.Fatalf("%s: %d operations failed", name, failures.Load())
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples
	}

	pct := func(s []time.Duration, q int) time.Duration {
		if len(s) == 0 {
			return 0
		}
		return s[(len(s)-1)*q/100]
	}
	report := func(name string, s []time.Duration) {
		t.Logf("%-22s n=%-6d p50=%-10v p90=%-10v p99=%-10v max=%v",
			name, len(s),
			pct(s, 50).Round(time.Microsecond), pct(s, 90).Round(time.Microsecond),
			pct(s, 99).Round(time.Millisecond), s[len(s)-1].Round(time.Millisecond))
	}

	// M5: single-row upsert on the largest rows. Writes to a scratch column set
	// so the loaded corpus stays usable for the other shapes.
	upsert := run("upsert", func(tg target, i int) error {
		_, err := store.Write(ctx, tg.id, tg.server, map[string]any{
			"userAutoLive": map[string]any{"count": i},
		}, WriteMysekai, DefaultLimits())
		return err
	})
	allow := run("allowlist read", func(tg target, _ int) error {
		_, err := store.Fetch(ctx, tg.id, tg.server, allowlist)
		return err
	})
	whole := run("whole-row read", func(tg target, _ int) error {
		_, err := store.Fetch(ctx, tg.id, tg.server, nil)
		return err
	})

	report("M5 upsert", upsert)
	report("allowlist read (25)", allow)
	report("whole-row read (246)", whole)

	st := pool.Stat()
	waited := st.EmptyAcquireCount() - baseline
	t.Logf("M6: waited=%d (total=%d, warm-up baseline=%d)  Canceled=%d  TotalConns=%d/%d",
		waited, st.EmptyAcquireCount(), baseline, st.CanceledAcquireCount(),
		st.TotalConns(), maxConns)

	samples := upsert
	p := func(q int) time.Duration { return pct(samples, q) }

	if p(99) >= 200*time.Millisecond {
		t.Fatalf("M5 FAILED: p99 = %v, gate is < 200 ms", p(99))
	}
	if waited != 0 {
		t.Fatalf("M6 FAILED: %d acquires waited on an empty pool at %d workers (gate is 0 above the warm-up baseline)",
			waited, workers)
	}
}
