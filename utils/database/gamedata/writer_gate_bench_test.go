package gamedata

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// Measures what the change gate saves on a JP-sized synthetic suite row:
// write wall time and WAL bytes per upload, with the gate off, with the gated
// columns unchanged, and with them changed on every upload.
//
//	GAMEDATA_WRITE_GATE_BENCH_PG=postgres://...   (a disposable database)
func TestWriteGateBench(t *testing.T) {
	dsn := os.Getenv("GAMEDATA_WRITE_GATE_BENCH_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_WRITE_GATE_BENCH_PG to measure the suite change gate")
	}
	ctx := context.Background()
	pool, err := NewPool(ctx, PoolConfig{URL: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	cat := catalog.Suite()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+catalog.QuoteIdent(cat.Table)); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSchema(ctx, pool, true, cat); err != nil {
		t.Fatal(err)
	}
	s := NewStore(pool, cat)

	const writes = 24
	saved := changeGatedSuiteKeys
	defer func() { changeGatedSuiteKeys = saved }()

	for _, v := range []struct {
		name    string
		gated   []string
		changed bool
	}{
		{"gate off (previous behaviour)", nil, false},
		{"gate on, costume columns unchanged", saved, false},
		{"gate on, costume columns changed every upload", saved, true},
	} {
		changeGatedSuiteKeys = v.gated
		id := int64(len(v.name))
		mustWrite(t, s, ctx, id, "jp", benchSuiteDoc(0, "have"), WriteSuite)
		durations := make([]time.Duration, 0, writes)
		var walTotal int64
		for i := 1; i <= writes; i++ {
			variant := "have"
			if v.changed && i%2 == 1 {
				variant = "none"
			}
			doc := benchSuiteDoc(int64(i), variant)
			var before string
			if err := pool.QueryRow(ctx, `SELECT pg_current_wal_insert_lsn()::text`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			mustWrite(t, s, ctx, id, "jp", doc, WriteSuite)
			durations = append(durations, time.Since(start))
			var wal int64
			if err := pool.QueryRow(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(), $1::pg_lsn)::bigint`, before).Scan(&wal); err != nil {
				t.Fatal(err)
			}
			walTotal += wal
		}
		slices.Sort(durations)
		t.Logf("%-46s p50 %6.1f ms  p90 %6.1f ms  WAL %6.2f MB/write",
			v.name,
			float64(durations[len(durations)/2].Microseconds())/1000,
			float64(durations[len(durations)*9/10].Microseconds())/1000,
			float64(walTotal)/float64(writes)/1e6)
	}
}

// benchSuiteDoc is a synthetic JP-sized suite document: the two costume
// catalogues at production element counts plus enough other columns to make
// up the rest of a large row.
func benchSuiteDoc(i int64, costumeVariant string) map[string]any {
	statuses := make([]any, 0, 84000)
	for k := range 84000 {
		status := "have"
		if k == 42000 {
			status = costumeVariant
		}
		statuses = append(statuses, map[string]any{
			"costume3dId": int64(100000 + k), "status": status, "obtainedAt": int64(1600000000000 + int64(k)*86413),
		})
	}
	shop := make([]any, 0, 30000)
	for k := range 30000 {
		shop = append(shop, map[string]any{"costume3dShopItemId": int64(k + 1), "status": "bought"})
	}
	cards := make([]any, 0, 1500)
	for k := range 1500 {
		cards = append(cards, map[string]any{
			"cardId": k, "level": 60, "exp": k * 13, "masterRank": k % 6, "createdAt": 1600000000000 + i*1000 + int64(k),
			"episodes": []any{map[string]any{"cardEpisodeId": k * 2, "scenarioStatus": "already_read"}},
		})
	}
	results := make([]any, 0, 9000)
	for k := range 9000 {
		results = append(results, map[string]any{
			"musicId": k / 5, "musicDifficultyType": "master", "playType": "single", "playResult": "full_combo",
			"highScore": 900000 + int(i)*10 + k, "updatedAt": 1600000000000 + i*1000 + int64(k),
		})
	}
	missions := make([]any, 0, 6000)
	for k := range 6000 {
		missions = append(missions, map[string]any{"missionId": k, "missionType": "normal", "progress": int(i) + k, "missionStatus": "achieved"})
	}
	return map[string]any{
		"upload_time":            1700000000 + i,
		"userCostume3dStatuses":  statuses,
		"userCostume3dShopItems": shop,
		"userCards":              cards,
		"userMusicResults":       results,
		"userMissionStatuses":    missions,
	}
}
