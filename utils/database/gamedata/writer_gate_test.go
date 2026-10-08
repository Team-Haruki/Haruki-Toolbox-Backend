package gamedata

import (
	"bytes"
	"context"
	"crypto/sha256"
	json "encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/gamemerge"
)

const (
	colCostumeStatuses = "user_costume3d_statuses_j"
	colCostumeShop     = "user_costume3d_shop_items_j"
)

// encodeGated is how the writer encodes a change-gated column.
func encodeGated(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func digestOf(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

func encodeSuiteForTest(t *testing.T, data map[string]any) (*Store, *encoded, *WriteStats) {
	t.Helper()
	s := NewStore(nil, catalog.Suite())
	stats := &WriteStats{}
	enc, err := s.encode(data, WriteSuite, stats)
	if err != nil {
		t.Fatal(err)
	}
	return s, enc, stats
}

// The gate list must stay away from merged history keys and must resolve to
// real suite columns; a typo would silently disable the gate.
func TestChangeGatedSuiteKeysResolveToPlainColumns(t *testing.T) {
	for _, k := range changeGatedSuiteKeys {
		if gamemerge.IsMergedKey(k) {
			t.Fatalf("%s is a merged history key and must not be gated", k)
		}
		if _, place := catalog.Suite().Resolve(k); place != catalog.PlaceColumn {
			t.Fatalf("%s does not resolve to a suite column", k)
		}
	}
}

func TestGatedColumnsOnlyListsCarriedColumnsInOrder(t *testing.T) {
	s, enc, _ := encodeSuiteForTest(t, map[string]any{
		"userCards":              []any{map[string]any{"cardId": 1}},
		"userCostume3dStatuses":  []any{map[string]any{"costume3dId": 1}},
		"userCostume3dShopItems": []any{map[string]any{"costume3dShopItemId": 1}},
	})
	got := s.gatedColumns(enc)
	want := []string{colCostumeShop, colCostumeStatuses}
	if !slices.Equal(got, want) {
		t.Fatalf("gated = %v, want %v", got, want)
	}

	s, enc, _ = encodeSuiteForTest(t, map[string]any{"userCards": []any{}})
	if got := s.gatedColumns(enc); len(got) != 0 {
		t.Fatalf("upload without gated keys gated %v", got)
	}

	// The compact spelling lands in the same column and is gated too.
	s, enc, _ = encodeSuiteForTest(t, map[string]any{
		"compactUserCostume3dStatuses": map[string]any{"__ENUM__": map[string]any{}},
	})
	if got := s.gatedColumns(enc); !slices.Equal(got, []string{colCostumeStatuses}) {
		t.Fatalf("compact spelling gated = %v", got)
	}
}

func TestSkipUnchangedColumnsComparesFinalBytes(t *testing.T) {
	s, enc, stats := encodeSuiteForTest(t, map[string]any{
		"userCostume3dStatuses":  []any{map[string]any{"costume3dId": 1}},
		"userCostume3dShopItems": []any{map[string]any{"costume3dShopItemId": 1}},
	})
	gated := s.gatedColumns(enc)
	statuses := enc.columns[colCostumeStatuses]
	stored := map[string][]byte{
		colCostumeStatuses: digestOf(statuses),
		colCostumeShop:     digestOf([]byte(`[{"costume3dShopItemId":2}]`)),
	}
	skipUnchangedColumns(enc, gated, stored, stats)

	if enc.columns[colCostumeStatuses] != nil {
		t.Fatal("unchanged column was not skipped")
	}
	if enc.columns[colCostumeShop] == nil {
		t.Fatal("changed column was skipped")
	}
	if stats.UnchangedColumns != 1 || stats.UnchangedBytes != len(statuses) {
		t.Fatalf("stats = %d cols / %d bytes, want 1 / %d", stats.UnchangedColumns, stats.UnchangedBytes, len(statuses))
	}
}

// No stored digest (first upload, or a NULL column) always writes.
func TestSkipUnchangedColumnsWritesWithoutStoredDigest(t *testing.T) {
	s, enc, stats := encodeSuiteForTest(t, map[string]any{
		"userCostume3dStatuses": []any{map[string]any{"costume3dId": 1}},
	})
	skipUnchangedColumns(enc, s.gatedColumns(enc), map[string][]byte{}, stats)
	if enc.columns[colCostumeStatuses] == nil || stats.UnchangedColumns != 0 {
		t.Fatal("a column with no stored digest was skipped")
	}
}

// A skipped column stays in the statement as a NULL parameter under
// COALESCE, so the stored value is kept and the statement text is the same
// whether or not the value changed.
func TestUpsertStatementKeepsSkippedColumnAsNull(t *testing.T) {
	data := map[string]any{
		"userCards":             []any{map[string]any{"cardId": 1}},
		"userCostume3dStatuses": []any{map[string]any{"costume3dId": 1}},
	}
	s, enc, stats := encodeSuiteForTest(t, data)
	changedSQL, _ := s.upsertStatement(1, 1, enc, clearNone)

	skipUnchangedColumns(enc, s.gatedColumns(enc), map[string][]byte{
		colCostumeStatuses: digestOf(enc.columns[colCostumeStatuses]),
	}, stats)
	sql, args := s.upsertStatement(1, 1, enc, clearNone)
	if sql != changedSQL {
		t.Fatal("skipping a column changed the statement text")
	}
	q := catalog.QuoteIdent(colCostumeStatuses)
	if !strings.Contains(sql, q+" = COALESCE(EXCLUDED."+q) {
		t.Fatalf("gated column is not merged with COALESCE: %s", sql)
	}
	cols := []string{catalog.ColUserID, catalog.ColServer, catalog.ColUploadTime, catalog.ExtraColumn}
	cols = append(cols, enc.order...)
	slices.Sort(cols[4:])
	i := slices.Index(cols, colCostumeStatuses)
	if i < 0 || args[i] != nil {
		t.Fatalf("skipped column argument = %v, want NULL", args[i])
	}
	if j := slices.Index(cols, "user_cards_j"); j < 0 || args[j] == nil {
		t.Fatal("ungated column lost its value")
	}
}

// Uploads decode to Go maps, whose iteration order is random. A gated column
// must encode the same value to the same bytes every time, or its digest would
// never match; every other column keeps the default encoding.
func TestGatedColumnEncodingIsStable(t *testing.T) {
	element := map[string]any{"costume3dId": 1, "status": "have", "obtainedAt": 2, "a": 3, "b": 4, "c": 5}
	data := map[string]any{
		"userCostume3dStatuses": []any{element},
		"userCards":             []any{element},
	}
	_, first, _ := encodeSuiteForTest(t, data)
	want := string(first.columns[colCostumeStatuses])
	if want != encodeGated(t, []any{element}) {
		t.Fatalf("gated column not encoded with sorted keys: %s", want)
	}
	for range 30 {
		_, enc, _ := encodeSuiteForTest(t, data)
		if got := string(enc.columns[colCostumeStatuses]); got != want {
			t.Fatalf("gated column encoding changed between encodes:\n%s\n%s", got, want)
		}
	}
	// WriteMigrate keeps the default encoding for every column.
	s := NewStore(nil, catalog.Suite())
	if s.isGatedColumn("user_cards_j") || !s.isGatedColumn(colCostumeShop) {
		t.Fatal("isGatedColumn disagrees with changeGatedSuiteKeys")
	}
	b, err := s.encodeColumnValue(WriteMigrate, colCostumeStatuses, []any{1})
	if err != nil || string(b) != "[1]" {
		t.Fatalf("migrate encode = %s, %v", b, err)
	}
	if _, err := s.encodeColumnValue(WriteSuite, colCostumeStatuses, func() {}); err == nil {
		t.Fatal("unencodable gated value did not error")
	}
}

// fakeDigestTx answers the digest query without a database. Only QueryRow is
// implemented; any other pgx.Tx method would panic on the nil embedded value.
type fakeDigestTx struct {
	pgx.Tx
	sql  string
	args []any
	row  fakeDigestRow
}

func (f *fakeDigestTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.sql, f.args = sql, args
	return f.row
}

type fakeDigestRow struct {
	values [][]byte
	err    error
}

func (r fakeDigestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		*(d.(*[]byte)) = r.values[i]
	}
	return nil
}

func TestReadColumnDigests(t *testing.T) {
	ctx := context.Background()
	cols := []string{colCostumeShop, colCostumeStatuses}
	digest := digestOf([]byte("[]"))

	tx := &fakeDigestTx{row: fakeDigestRow{values: [][]byte{nil, digest}}}
	got, err := readColumnDigests(ctx, tx, "game_suite", 7, 1, cols)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[colCostumeShop]; ok {
		t.Fatal("a NULL column produced a digest")
	}
	if !bytes.Equal(got[colCostumeStatuses], digest) {
		t.Fatal("stored digest not returned")
	}
	// The comparison must hold the row lock until the upsert.
	if !strings.HasSuffix(tx.sql, "FOR UPDATE") || !strings.Contains(tx.sql, `sha256(convert_to("user_costume3d_statuses_j"::text, 'UTF8'))`) {
		t.Fatalf("digest query: %s", tx.sql)
	}
	if tx.args[0] != int64(7) || tx.args[1] != int16(1) {
		t.Fatalf("digest query args = %v", tx.args)
	}

	// No row yet: no digests, so everything is written.
	got, err = readColumnDigests(ctx, &fakeDigestTx{row: fakeDigestRow{err: pgx.ErrNoRows}}, "game_suite", 7, 1, cols)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing row: %v, %v", got, err)
	}

	if _, err := readColumnDigests(ctx, &fakeDigestTx{row: fakeDigestRow{err: errors.New("boom")}}, "game_suite", 7, 1, cols); err == nil {
		t.Fatal("query failure was swallowed")
	}
}
