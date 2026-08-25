package gamedata

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// The write path against a REAL PostgreSQL.
//
// Everything else about the writer is unit-tested on the encode half: the SQL it
// builds, which columns it names, what it drops. None of that executes the
// statement, so the parts that only exist at runtime — the COALESCE merge
// actually preserving a column, the suite transaction reading the stored history
// back before merging it — have never run.
//
// A read bug serves a wrong response; a write bug corrupts the row and the next
// upload builds on it.
//
//	GAMEDATA_WRITE_TEST_PG=postgres://...   (a database this test may CREATE and DROP tables in)
func writeTestStore(t *testing.T, cat *catalog.Catalog) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("GAMEDATA_WRITE_TEST_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_WRITE_TEST_PG to run the write-path integration tests")
	}
	ctx := context.Background()
	pool, err := NewPool(ctx, PoolConfig{URL: dsn, MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+catalog.QuoteIdent(cat.Table)); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSchema(ctx, pool, true, cat); err != nil {
		t.Fatal(err)
	}
	return NewStore(pool, cat), ctx
}

func mustWrite(t *testing.T, s *Store, ctx context.Context, id int64, server string, data map[string]any, mode WriteMode) WriteStats {
	t.Helper()
	st, err := s.Write(ctx, id, server, data, mode, DefaultLimits())
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return st
}

func mustValue(t *testing.T, s *Store, ctx context.Context, id int64, server, key string) (string, bool) {
	t.Helper()
	row, err := s.Fetch(ctx, id, server, nil)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	raw, ok, err := row.RawValue(key)
	if err != nil {
		t.Fatalf("value %s: %v", key, err)
	}
	return string(raw), ok
}

// `$set` is a top-level MERGE. A second upload that omits a key must not clear
// it — that is the difference between COALESCE and a plain EXCLUDED assignment,
// and getting it wrong silently deletes data on every partial upload.
func TestWriteMergePreservesOmittedColumns(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(28808221489823746), "jp"

	mustWrite(t, s, ctx, id, server, map[string]any{
		"userCards": []any{map[string]any{"cardId": 1}},
		"userDecks": []any{map[string]any{"deckId": 9}},
	}, WriteMysekai)

	// Second upload carries only userCards.
	mustWrite(t, s, ctx, id, server, map[string]any{
		"userCards": []any{map[string]any{"cardId": 2}},
	}, WriteMysekai)

	cards, ok := mustValue(t, s, ctx, id, server, "userCards")
	if !ok || cards != `[{"cardId":2}]` {
		t.Fatalf("userCards = %s (ok=%v), want the newer value", cards, ok)
	}
	decks, ok := mustValue(t, s, ctx, id, server, "userDecks")
	if !ok {
		t.Fatal("userDecks was CLEARED by an upload that did not mention it")
	}
	if decks != `[{"deckId":9}]` {
		t.Fatalf("userDecks = %s", decks)
	}
}

// Migration replaces the whole row, so a re-run rebuilds it instead of leaving
// values no longer present in the source.
func TestWriteMigrateClearsAbsentColumns(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(1), "jp"

	mustWrite(t, s, ctx, id, server, map[string]any{
		"userCards": []any{1}, "userDecks": []any{2},
	}, WriteMigrate)
	mustWrite(t, s, ctx, id, server, map[string]any{
		"userCards": []any{3},
	}, WriteMigrate)

	if _, ok := mustValue(t, s, ctx, id, server, "userDecks"); ok {
		t.Fatal("a migration re-run left a column the source no longer carries")
	}
}

// The three history keys accumulate across uploads: a client only sends the
// events it currently knows about, so replacing would erase a player's history
// the first time an old event drops off.
func TestWriteSuiteAccumulatesHistory(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(7), "jp"

	mustWrite(t, s, ctx, id, server, map[string]any{
		"userEvents": []any{map[string]any{"eventId": 1, "eventPoint": 100}},
	}, WriteSuite)

	// A later upload mentions only event 2. Event 1 must survive.
	mustWrite(t, s, ctx, id, server, map[string]any{
		"userEvents": []any{map[string]any{"eventId": 2, "eventPoint": 5}},
	}, WriteSuite)

	raw, ok := mustValue(t, s, ctx, id, server, "userEvents")
	if !ok {
		t.Fatal("userEvents missing")
	}
	var events []map[string]any
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("history did not accumulate: %s", raw)
	}
}

// A higher eventPoint wins even when it arrives FIRST — the merge is not
// last-write-wins, and this is the path that reads the stored side back.
func TestWriteSuiteKeepsTheHigherEventPoint(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(8), "jp"

	mustWrite(t, s, ctx, id, server, map[string]any{
		"userEvents": []any{map[string]any{"eventId": 1, "eventPoint": 2000000}},
	}, WriteSuite)
	mustWrite(t, s, ctx, id, server, map[string]any{
		"userEvents": []any{map[string]any{"eventId": 1, "eventPoint": 5}},
	}, WriteSuite)

	raw, _ := mustValue(t, s, ctx, id, server, "userEvents")
	var events []map[string]any
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event: %s", raw)
	}
	if p, _ := events[0]["eventPoint"].(float64); p != 2000000 {
		t.Fatalf("the lower eventPoint overwrote the higher one: %s", raw)
	}
}

// A game user id above 2^53 must survive the write/read round trip intact.
func TestWriteKeepsLargeIdentitiesExact(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(9), "jp"
	const big = int64(28808221489823746)

	mustWrite(t, s, ctx, id, server, map[string]any{
		"userEvents": []any{map[string]any{"eventId": big, "eventPoint": 1}},
	}, WriteSuite)
	// A second write forces the stored side through decode -> merge -> encode.
	mustWrite(t, s, ctx, id, server, map[string]any{
		"userEvents": []any{map[string]any{"eventId": big, "eventPoint": 2}},
	}, WriteSuite)

	raw, _ := mustValue(t, s, ctx, id, server, "userEvents")
	if !containsSub(raw, "28808221489823746") {
		t.Fatalf("the identity lost precision through the merge: %s", raw)
	}
}

// Denied keys must not reach the database even when the statement executes.
func TestWriteNeverPersistsDeniedKeys(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(10), "jp"

	st := mustWrite(t, s, ctx, id, server, map[string]any{
		"userCards":        []any{1},
		"userRegistration": map[string]any{"age": 24, "signature": "jwt"},
	}, WriteSuite)
	if st.DeniedDropped["userRegistration"] != 1 {
		t.Fatalf("denied drop not counted: %v", st.DeniedDropped)
	}

	row, err := s.Fetch(ctx, id, server, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := row.RawValue("userRegistration"); ok {
		t.Fatal("a denied key was persisted")
	}
	body, err := row.PrivateBody(nil)
	if err != nil {
		t.Fatal(err)
	}
	if containsSub(string(body), "signature") || containsSub(string(body), "userRegistration") {
		t.Fatalf("a denied key reached the response: %s", body)
	}
}

// The birthday-party write owns three columns and must leave the rest alone.
func TestWriteBirthdayPartyTouchesOnlyItsColumns(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Mysekai())
	const id, server = int64(11), "jp"

	mustWrite(t, s, ctx, id, server, map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiPhotos":      []any{map[string]any{"id": 1}},
			"userMysekaiHarvestMaps": []any{map[string]any{"m": 1}},
		},
	}, WriteMysekai)

	mustWrite(t, s, ctx, id, server, map[string]any{
		"upload_time": int64(1758686145),
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": []any{map[string]any{"m": 2}},
		},
	}, WriteBirthdayParty)

	harvest, ok := mustValue(t, s, ctx, id, server, "updatedResources.userMysekaiHarvestMaps")
	if !ok || harvest != `[{"m":2}]` {
		t.Fatalf("harvest map = %s (ok=%v)", harvest, ok)
	}
	photos, ok := mustValue(t, s, ctx, id, server, "updatedResources.userMysekaiPhotos")
	if !ok {
		t.Fatal("the birthday-party write CLEARED an unrelated column")
	}
	if photos != `[{"id":1}]` {
		t.Fatalf("photos = %s", photos)
	}
}

// Upsert must create the row on a first upload, not require one to exist.
func TestWriteCreatesTheRowOnFirstUpload(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id, server = int64(12), "cn"
	if _, err := s.Fetch(ctx, id, server, nil); err == nil {
		t.Fatal("expected no row before the first write")
	}
	mustWrite(t, s, ctx, id, server, map[string]any{"userCards": []any{1}}, WriteSuite)
	if _, ok := mustValue(t, s, ctx, id, server, "userCards"); !ok {
		t.Fatal("the first upload did not create the row")
	}
}

// The composite primary key must keep two regions of one account apart.
func TestWriteKeepsRegionsSeparate(t *testing.T) {
	s, ctx := writeTestStore(t, catalog.Suite())
	const id = int64(13)

	mustWrite(t, s, ctx, id, "jp", map[string]any{"userCards": []any{1}}, WriteSuite)
	mustWrite(t, s, ctx, id, "tw", map[string]any{"userCards": []any{2}}, WriteSuite)

	jp, _ := mustValue(t, s, ctx, id, "jp", "userCards")
	tw, _ := mustValue(t, s, ctx, id, "tw", "userCards")
	if jp != `[1]` || tw != `[2]` {
		t.Fatalf("regions bled into each other: jp=%s tw=%s", jp, tw)
	}
}
