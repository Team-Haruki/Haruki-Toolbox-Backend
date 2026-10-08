package gamedata

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// The change gate against a REAL PostgreSQL (GAMEDATA_WRITE_TEST_PG): an
// unchanged gated column keeps its stored value and TOAST pointer, a changed
// one is rewritten, and the first upload writes everything.

// costumeStatuses builds a value large enough to be stored out of line, so
// the TOAST pointer comparison below means something. variant changes one
// element's status.
func costumeStatuses(n int, variant string) []any {
	out := make([]any, 0, n)
	for i := range n {
		status := "have"
		if i == n/2 {
			status = variant
		}
		out = append(out, map[string]any{
			"costume3dId": int64(100000 + i*7),
			"status":      status,
			"obtainedAt":  int64(1600000000000 + int64(i)*86413),
		})
	}
	return out
}

func gateUpload(uploadTime int64, statusVariant string) map[string]any {
	return map[string]any{
		"upload_time":            uploadTime,
		"userCards":              []any{map[string]any{"cardId": uploadTime}},
		"userCostume3dStatuses":  costumeStatuses(4000, statusVariant),
		"userCostume3dShopItems": []any{map[string]any{"costume3dShopItemId": 1, "status": "bought"}},
	}
}

// toastChunkID returns the TOAST value id the column points at, or "" when it
// is stored inline. pg_column_toast_chunk_id exists from PostgreSQL 17 on;
// older servers skip the pointer assertion.
func toastChunkID(t *testing.T, s *Store, ctx context.Context, id int64, server, col string) (string, bool) {
	t.Helper()
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regproc('pg_column_toast_chunk_id') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		return "", false
	}
	code, _ := catalog.ServerCode(server)
	var chunk *string
	sql := fmt.Sprintf(`SELECT pg_column_toast_chunk_id(%s)::text FROM %s WHERE user_id = $1 AND server = $2`,
		catalog.QuoteIdent(col), catalog.QuoteIdent(s.cat.Table))
	if err := s.pool.QueryRow(ctx, sql, id, code).Scan(&chunk); err != nil {
		t.Fatal(err)
	}
	if chunk == nil {
		t.Fatalf("%s is stored inline; the fixture is too small to test TOAST reuse", col)
	}
	return *chunk, true
}

func TestWriteGateFirstUploadWritesEverything(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(9001), "jp"

	st := mustWrite(t, s, ctx, id, server, gateUpload(1000, "have"), WriteSuite)
	if st.UnchangedColumns != 0 {
		t.Fatalf("first upload skipped %d columns", st.UnchangedColumns)
	}
	want := encodeGated(t, costumeStatuses(4000, "have"))
	if got, ok := mustValue(t, s, ctx, id, server, "userCostume3dStatuses"); !ok || got != string(want) {
		t.Fatal("first upload did not store the costume statuses")
	}
}

func TestWriteGateUnchangedColumnKeepsStoredValue(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(9002), "jp"

	mustWrite(t, s, ctx, id, server, gateUpload(1000, "have"), WriteSuite)
	before, havePointer := toastChunkID(t, s, ctx, id, server, colCostumeStatuses)

	st := mustWrite(t, s, ctx, id, server, gateUpload(2000, "have"), WriteSuite)
	if st.UnchangedColumns != 2 {
		t.Fatalf("unchanged columns = %d, want 2", st.UnchangedColumns)
	}
	if havePointer {
		after, _ := toastChunkID(t, s, ctx, id, server, colCostumeStatuses)
		if after != before {
			t.Fatalf("unchanged column was rewritten: TOAST value %s -> %s", before, after)
		}
	}
	want := encodeGated(t, costumeStatuses(4000, "have"))
	if got, _ := mustValue(t, s, ctx, id, server, "userCostume3dStatuses"); got != string(want) {
		t.Fatal("unchanged column lost its value")
	}
	// Everything outside the gate is still written.
	if got, _ := mustValue(t, s, ctx, id, server, "userCards"); got != `[{"cardId":2000}]` {
		t.Fatalf("ungated column not written: %s", got)
	}
	if got, _ := mustValue(t, s, ctx, id, server, "upload_time"); got != "2000" {
		t.Fatalf("upload_time = %s, want 2000", got)
	}
}

func TestWriteGateChangedColumnIsRewritten(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(9003), "jp"

	mustWrite(t, s, ctx, id, server, gateUpload(1000, "have"), WriteSuite)
	before, havePointer := toastChunkID(t, s, ctx, id, server, colCostumeStatuses)

	st := mustWrite(t, s, ctx, id, server, gateUpload(2000, "none"), WriteSuite)
	if st.UnchangedColumns != 1 { // the shop items did not change
		t.Fatalf("unchanged columns = %d, want 1", st.UnchangedColumns)
	}
	want := encodeGated(t, costumeStatuses(4000, "none"))
	if got, _ := mustValue(t, s, ctx, id, server, "userCostume3dStatuses"); got != string(want) {
		t.Fatal("changed column was not written")
	}
	if havePointer {
		after, _ := toastChunkID(t, s, ctx, id, server, colCostumeStatuses)
		if after == before {
			t.Fatal("changed column kept its old TOAST value")
		}
	}
}

// A value repaired or rewritten outside the upload path is compared like any
// other: nothing is cached, so nothing goes stale.
func TestWriteGateComparesAgainstOutOfBandEdits(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(9004), "jp"

	mustWrite(t, s, ctx, id, server, gateUpload(1000, "have"), WriteSuite)
	code, _ := catalog.ServerCode(server)
	if _, err := s.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET %s = '[]' WHERE user_id = $1 AND server = $2`,
		catalog.QuoteIdent(s.cat.Table), catalog.QuoteIdent(colCostumeStatuses)), id, code); err != nil {
		t.Fatal(err)
	}
	st := mustWrite(t, s, ctx, id, server, gateUpload(2000, "have"), WriteSuite)
	if st.UnchangedColumns != 1 {
		t.Fatalf("unchanged columns = %d, want 1 (only the shop items)", st.UnchangedColumns)
	}
	want := encodeGated(t, costumeStatuses(4000, "have"))
	if got, _ := mustValue(t, s, ctx, id, server, "userCostume3dStatuses"); got != string(want) {
		t.Fatal("the upload did not overwrite the out-of-band edit")
	}
}

// The gate shares the history keys' transaction and row lock.
func TestWriteGateWithHistoryKeys(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(9005), "jp"

	first := gateUpload(1000, "have")
	first["userEvents"] = []any{map[string]any{"eventId": 1, "eventPoint": 10}}
	mustWrite(t, s, ctx, id, server, first, WriteSuite)

	second := gateUpload(2000, "have")
	second["userEvents"] = []any{map[string]any{"eventId": 2, "eventPoint": 20}}
	st := mustWrite(t, s, ctx, id, server, second, WriteSuite)
	if st.UnchangedColumns != 2 {
		t.Fatalf("unchanged columns = %d, want 2", st.UnchangedColumns)
	}
	events, _ := mustValue(t, s, ctx, id, server, "userEvents")
	for _, want := range []string{`"eventId":1`, `"eventId":2`} {
		if !containsSub(events, want) {
			t.Fatalf("history merge lost %s: %s", want, events)
		}
	}
}

// Concurrent uploads stay serialized. Each upload pairs its upload_time with
// one of two costume values; if a comparison could ever see a digest another
// transaction was about to replace, the final row would pair one upload's
// upload_time with the other upload's costume value.
func TestWriteGateConcurrentUploadsStayConsistent(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(9006), "jp"
	variants := []string{"have", "none"}

	mustWrite(t, s, ctx, id, server, gateUpload(1, variants[1]), WriteSuite)
	for round := range 5 {
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for i := range 16 {
			wg.Add(1)
			go func(ut int64) {
				defer wg.Done()
				if _, err := s.Write(ctx, id, server, gateUpload(ut, variants[ut%2]), WriteSuite, DefaultLimits()); err != nil {
					errs <- err
				}
			}(int64(round*100 + i + 2))
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		row, err := s.Fetch(ctx, id, server, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, _, _ := row.RawValue("userCostume3dStatuses")
		want := encodeGated(t, costumeStatuses(4000, variants[row.UploadTime%2]))
		if string(got) != string(want) {
			t.Fatalf("round %d: upload_time %d stored with the other upload's costume value", round, row.UploadTime)
		}
	}
}
