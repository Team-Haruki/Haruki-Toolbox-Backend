package gamedata

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/gamemerge"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type historyReadTrace struct {
	sync.Mutex
	queries []string
}

func (trace *historyReadTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT ") && strings.HasSuffix(data.SQL, " FOR UPDATE") {
		trace.Lock()
		trace.queries = append(trace.queries, data.SQL)
		trace.Unlock()
	}
	return ctx
}
func (*historyReadTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (trace *historyReadTrace) take() []string {
	trace.Lock()
	defer trace.Unlock()
	out := trace.queries
	trace.queries = nil
	return out
}

// Capture executed SQL, then verify the resulting values against real PostgreSQL.
func TestHistoryProjectionReadsOnlyUploadedKeys(t *testing.T) {
	original, ctx := writeTestStore(t, catalog.Suite())
	const id = int64(28808221489823746)
	initial := map[string]any{
		gamemerge.KeyUserEvents:      []any{map[string]any{"eventId": 1, "eventPoint": 10}},
		gamemerge.KeyUserWorldBlooms: []any{map[string]any{"eventId": 2, "gameCharacterId": 3, "worldBloomChapterPoint": 20}},
		gamemerge.KeyUserGachas:      []any{map[string]any{"gachaId": 4, "gachaBehaviorId": 5, "lastSpinAt": 30}},
	}
	mustWrite(t, original, ctx, id, "cn", initial, WriteMysekai)
	before := map[string]string{}
	for _, key := range gamemerge.Keys() {
		before[key], _ = mustValue(t, original, ctx, id, "cn", key)
	}
	trace := &historyReadTrace{}
	cfg, err := resolvePoolConfig(PoolConfig{URL: os.Getenv("GAMEDATA_WRITE_TEST_PG"), MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := NewStore(&Pool{Pool: pool}, catalog.Suite())
	keys := gamemerge.Keys()
	for mask := 1; mask < 8; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			upload := map[string]any{}
			// Explicit reverse insertion must still yield a canonical SELECT order.
			for i := len(keys) - 1; i >= 0; i-- {
				if mask&(1<<i) != 0 {
					upload[keys[i]] = initial[keys[i]]
				}
			}
			prior := map[string]string{}
			for _, key := range keys {
				prior[key], _ = mustValue(t, s, ctx, id, "cn", key)
			}
			trace.take()
			mustWrite(t, s, ctx, id, "cn", upload, WriteSuite)
			queries := trace.take()
			if len(queries) != 1 {
				t.Fatalf("history reads=%d", len(queries))
			}
			cols := []string{}
			for i, key := range keys {
				if mask&(1<<i) != 0 {
					e, _ := s.cat.Resolve(key)
					cols = append(cols, catalog.QuoteIdent(e.Column))
				}
			}
			want := fmt.Sprintf(`SELECT %s FROM "game_suite" WHERE "user_id" = $1 AND "server" = $2 FOR UPDATE`, strings.Join(cols, ", "))
			if queries[0] != want {
				t.Fatalf("projection = %s; want %s", queries[0], want)
			}
			for _, key := range keys {
				after, ok := mustValue(t, s, ctx, id, "cn", key)
				wantValue, wantErr := decodeJSONNumbers([]byte(before[key]))
				gotValue, gotErr := decodeJSONNumbers([]byte(after))
				if !ok || wantErr != nil || gotErr != nil || !reflect.DeepEqual(wantValue, gotValue) {
					t.Fatalf("history %s changed or disappeared: before=%s after=%s", key, before[key], after)
				}
				if _, uploaded := upload[key]; !uploaded && after != prior[key] {
					t.Fatalf("omitted history %s was rewritten", key)
				}
			}
		})
	}
	// Explicit nil still counts as an uploaded history key, and keeps old history.
	trace.take()
	mustWrite(t, s, ctx, id, "cn", map[string]any{gamemerge.KeyUserEvents: nil}, WriteSuite)
	queries := trace.take()
	if len(queries) != 1 {
		t.Fatalf("nil history skipped locking read: %v", queries)
	}
	events, _ := s.cat.Resolve(gamemerge.KeyUserEvents)
	if !strings.Contains(queries[0], catalog.QuoteIdent(events.Column)) {
		t.Fatal("nil uploaded key not read")
	}
	value, ok := mustValue(t, s, ctx, id, "cn", gamemerge.KeyUserEvents)
	wantValue, _ := decodeJSONNumbers([]byte(before[gamemerge.KeyUserEvents]))
	gotValue, err := decodeJSONNumbers([]byte(value))
	if !ok || err != nil || !reflect.DeepEqual(wantValue, gotValue) {
		t.Fatal("nil upload deleted stored events")
	}
}
