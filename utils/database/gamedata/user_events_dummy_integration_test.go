package gamedata

import (
	"context"
	json "encoding/json/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/jsonvalue"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Synthetic account; only the event ID and reported point values match the case.
const dummyEventUserID = int64(900178)
const dummyOldPoint = int64(100956916)
const dummySettlementPoint = int64(144520517)

func dummyEventUpload(point int64, rank int) map[string]any {
	event := map[string]any{"eventId": 178, "eventPoint": point}
	if rank > 0 {
		event["rank"] = rank
		event["rankingRewardReceivedAt"] = int64(1788756000)
	}
	return map[string]any{"userId": dummyEventUserID, "userEvents": []any{event}}
}

func dummyStoredPoint(t *testing.T, s *Store, ctx context.Context) (int64, int) {
	t.Helper()
	raw, ok := mustValue(t, s, ctx, dummyEventUserID, "cn", "userEvents")
	if !ok {
		t.Fatal("missing userEvents")
	}
	var events []struct {
		ID    int   `json:"eventId"`
		Point int64 `json:"eventPoint"`
		Rank  int   `json:"rank"`
	}
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatal(err)
	}
	matches := 0
	var point int64
	var rank int
	for _, e := range events {
		if e.ID == 178 {
			matches++
			point = e.Point
			rank = e.Rank
		}
	}
	if matches != 1 {
		t.Fatalf("want one event 178, got %d: %s", matches, raw)
	}
	t.Logf("stored event=178 point=%d rank=%d", point, rank)
	return point, rank
}

func TestUserEventsDummySequential(t *testing.T) {
	for _, format := range []string{"native", "json_numbers", "encrypted_msgpack"} {
		t.Run(format, func(t *testing.T) {
			s, ctx := writeTestStore(t, catalog.Suite())
			decode := func(data map[string]any) map[string]any {
				t.Helper()
				switch format {
				case "json_numbers":
					raw, err := json.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
					var out map[string]any
					if err = json.Unmarshal(raw, &out, jsonvalue.Numbers); err != nil {
						t.Fatal(err)
					}
					return out
				case "encrypted_msgpack":
					cryptor, err := sekai.NewSekaiCryptorFromHex("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "0102030405060708090a0b0c0d0e0f10")
					if err != nil {
						t.Fatal(err)
					}
					raw, err := cryptor.Pack(data)
					if err != nil {
						t.Fatal(err)
					}
					out, err := cryptor.Unpack(raw)
					if err != nil {
						t.Fatal(err)
					}
					return out.(map[string]any)
				}
				return data
			}
			mustWrite(t, s, ctx, dummyEventUserID, "cn", decode(dummyEventUpload(dummyOldPoint, 0)), WriteSuite)
			mustWrite(t, s, ctx, dummyEventUserID, "cn", decode(dummyEventUpload(dummySettlementPoint, 0)), WriteSuite)
			point, _ := dummyStoredPoint(t, s, ctx)
			if point != dummySettlementPoint {
				t.Fatalf("settlement update lost: %d", point)
			}
			mustWrite(t, s, ctx, dummyEventUserID, "cn", decode(dummyEventUpload(dummyOldPoint, 0)), WriteSuite)
			point, _ = dummyStoredPoint(t, s, ctx)
			if point != dummySettlementPoint {
				t.Fatalf("stale upload rolled point back: %d", point)
			}
		})
	}
}

func TestUserEventsDummySettlementMetadata(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	mustWrite(t, s, ctx, dummyEventUserID, "cn", dummyEventUpload(dummySettlementPoint, 10), WriteSuite)
	mustWrite(t, s, ctx, dummyEventUserID, "cn", dummyEventUpload(dummySettlementPoint, 12), WriteSuite)
	point, rank := dummyStoredPoint(t, s, ctx)
	if point != dummySettlementPoint || rank != 12 {
		t.Fatalf("same-point settlement metadata stayed stale: point=%d rank=%d", point, rank)
	}
}

type dummySlowUploadKey struct{}
type dummyPauseUpsert struct {
	reached chan uint32
	release chan struct{}
}

func (p *dummyPauseUpsert) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if ctx.Value(dummySlowUploadKey{}) == true && strings.Contains(data.SQL, "DO UPDATE SET") {
		select {
		case p.reached <- conn.PgConn().PID():
		case <-ctx.Done():
			return ctx
		}
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*dummyPauseUpsert) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Pause an old upload after its history read and before its final upsert. Let
// the settlement upload either commit (old implementation) or wait on the row
// lock (fixed implementation), then resume the old upload. No timing lottery.
func TestUserEventsDummyConcurrentStaleUpload(t *testing.T) {
	original, ctx := writeTestStore(t, catalog.Suite())
	mustWrite(t, original, ctx, dummyEventUserID, "cn", dummyEventUpload(dummyOldPoint, 0), WriteSuite)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pause := &dummyPauseUpsert{reached: make(chan uint32, 1), release: make(chan struct{})}
	cfg, err := resolvePoolConfig(PoolConfig{URL: os.Getenv("GAMEDATA_WRITE_TEST_PG"), MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = pause
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// Ensure timeout/cancellation releases the tracer before closing its pool.
	defer cancel()
	s := NewStore(&Pool{Pool: pool}, catalog.Suite())
	oldDone := make(chan error, 1)
	newDone := make(chan error, 1)
	go func() {
		_, err := s.Write(context.WithValue(ctx, dummySlowUploadKey{}, true), dummyEventUserID, "cn", dummyEventUpload(dummyOldPoint, 0), WriteSuite, DefaultLimits())
		oldDone <- err
	}()
	var oldPID uint32
	select {
	case oldPID = <-pause.reached:
	case err = <-oldDone:
		t.Fatalf("old upload exited before pause: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		_, err := s.Write(ctx, dummyEventUserID, "cn", dummyEventUpload(dummySettlementPoint, 0), WriteSuite, DefaultLimits())
		newDone <- err
	}()
	completed := false
waitForSettlement:
	for {
		select {
		case err = <-newDone:
			if err != nil {
				t.Fatal(err)
			}
			completed = true
			t.Log("settlement committed while stale upload was paused")
			break waitForSettlement
		default:
		}
		var waiting int
		if err = original.pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))", int(oldPID)).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			t.Log("settlement correctly waiting on stale upload row lock")
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(pause.release)
	if err = <-oldDone; err != nil {
		t.Fatal(err)
	}
	if !completed {
		if err = <-newDone; err != nil {
			t.Fatal(err)
		}
	}
	point, _ := dummyStoredPoint(t, s, ctx)
	if point != dummySettlementPoint {
		t.Fatalf("settlement overwritten by stale concurrent upload: got %d, want %d", point, dummySettlementPoint)
	}
}
