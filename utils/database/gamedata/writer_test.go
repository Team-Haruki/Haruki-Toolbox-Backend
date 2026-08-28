package gamedata

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

func suiteStore() *Store   { return NewStore(&Pool{}, catalog.Suite()) }
func mysekaiStore() *Store { return NewStore(&Pool{}, catalog.Mysekai()) }

// --- denied keys -------------------------------------------------------------

// Denied keys are dropped BEFORE encoding: they must not reach a column, must
// not reach `extra`, and must not even be rendered to bytes.
func TestDeniedKeysNeverReachAColumnOrExtra(t *testing.T) {
	s := suiteStore()
	data := map[string]any{
		"userCards":        []any{map[string]any{"cardId": 1}},
		"userRegistration": map[string]any{"age": 24, "signature": "jwt"},
		"userInherit":      []any{map[string]any{"x": 1}},
		"userPlatforms":    []any{"iOS"},
	}
	var stats WriteStats
	enc, err := s.encode(data, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	for k := range catalog.DeniedKeys {
		if e, place := s.cat.Resolve(k); place == catalog.PlaceColumn {
			if _, wrote := enc.columns[e.Column]; wrote {
				t.Fatalf("denied key %q reached column %q", k, e.Column)
			}
		}
	}
	if enc.extra != nil {
		if strings.Contains(string(enc.extra), "userRegistration") ||
			strings.Contains(string(enc.extra), "signature") {
			t.Fatalf("denied key leaked into extra: %s", enc.extra)
		}
	}
	if stats.DeniedDropped["userRegistration"] != 1 ||
		stats.DeniedDropped["userInherit"] != 1 ||
		stats.DeniedDropped["userPlatforms"] != 1 {
		t.Fatalf("denied drops not counted: %v", stats.DeniedDropped)
	}
}

// The same guard has to hold for a denied key nested under the flattened parent.
func TestDeniedFlattenedChildIsDropped(t *testing.T) {
	s := mysekaiStore()
	data := map[string]any{
		s.cat.FlattenKey: map[string]any{
			"userMysekaiPhotos": []any{1},
			"userRegistration":  map[string]any{"age": 24},
		},
	}
	var stats WriteStats
	enc, err := s.encode(data, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	if enc.extra != nil && strings.Contains(string(enc.extra), "userRegistration") {
		t.Fatalf("denied flattened child leaked into extra: %s", enc.extra)
	}
	if stats.DeniedDropped[s.cat.FlattenKey+".userRegistration"] != 1 {
		t.Fatalf("denied flattened child not counted: %v", stats.DeniedDropped)
	}
}

// --- merge vs replace --------------------------------------------------------

// The merge upsert must never mention a column the upload did not carry:
// listing it would write NULL and clear stored data on every partial upload.
func TestMergeUpsertOnlyTouchesSuppliedColumns(t *testing.T) {
	s := suiteStore()
	var stats WriteStats
	enc, err := s.encode(map[string]any{"userCards": []any{1}}, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	sql, args := s.upsertStatement(1, 1, enc, clearNone)
	if strings.Contains(sql, catalog.QuoteIdent("user_decks_j")) {
		t.Fatalf("merge statement mentions an unsupplied column:\n%s", sql)
	}
	if !strings.Contains(sql, "COALESCE(EXCLUDED.") {
		t.Fatalf("merge statement is not a COALESCE merge:\n%s", sql)
	}
	// user_id, server, upload_time, extra, user_cards_j
	if len(args) != 5 {
		t.Fatalf("args = %d, want 5: %v", len(args), args)
	}
}

// A mysekai upload replaces `updatedResources` wholesale on MongoDB, because it
// is one field there. Every flattened child must therefore be hard-assigned —
// including the ones the upload omitted, which is how a resource the player no
// longer has stops being reported. Merging them made PostgreSQL a union over
// every upload ever sent.
func TestMysekaiUpsertClearsAbsentFlattenedChildren(t *testing.T) {
	s := mysekaiStore()
	children := s.cat.FlattenChildren()
	if len(children) == 0 {
		t.Skip("mysekai catalog has no flattened children")
	}
	var stats WriteStats
	enc, err := s.encode(map[string]any{
		"updatedResources": map[string]any{children[0].Child: []any{1}},
	}, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := s.upsertStatement(1, 1, enc, clearFlattened)
	for _, e := range children {
		q := catalog.QuoteIdent(e.Column)
		if !strings.Contains(sql, q+" = EXCLUDED."+q) {
			t.Fatalf("flattened child %s is not cleared:\n%s", e.Column, sql)
		}
	}
	// `extra` holds the flattened children the catalog does not name, so it is
	// swapped out with the parent rather than merged.
	qx := catalog.QuoteIdent(catalog.ExtraColumn)
	if !strings.Contains(sql, qx+" = EXCLUDED."+qx) {
		t.Fatalf("extra is merged, so an unnamed child would survive:\n%s", sql)
	}
}

// The flattened clear must not leak into top-level columns: those really are
// merged by `$set`, and clearing one would delete data on a partial upload.
func TestMysekaiUpsertStillMergesTopLevelColumns(t *testing.T) {
	s := mysekaiStore()
	var top *catalog.Entry
	for i := range s.cat.Entries {
		if s.cat.Entries[i].Path == "" {
			top = &s.cat.Entries[i]
			break
		}
	}
	if top == nil {
		t.Skip("mysekai catalog has no top-level column")
	}
	var stats WriteStats
	enc, err := s.encode(map[string]any{top.Key: []any{1}}, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := s.upsertStatement(1, 1, enc, clearFlattened)
	q := catalog.QuoteIdent(top.Column)
	if !strings.Contains(sql, q+" = COALESCE(EXCLUDED."+q) {
		t.Fatalf("top-level column %s is no longer merged:\n%s", top.Column, sql)
	}
}

// A catalog with no flattened parent — suite — must be untouched by the scope,
// or every partial suite upload would start clearing columns.
func TestClearFlattenedIsANoOpWithoutAFlattenedParent(t *testing.T) {
	s := suiteStore()
	if len(s.cat.FlattenChildren()) != 0 {
		t.Skip("suite catalog gained a flattened parent")
	}
	var stats WriteStats
	enc, err := s.encode(map[string]any{"userCards": []any{1}}, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	flat, _ := s.upsertStatement(1, 1, enc, clearFlattened)
	merge, _ := s.upsertStatement(1, 1, enc, clearNone)
	if flat != merge {
		t.Fatalf("clearFlattened changed a catalog with no flattened parent:\n%s\n%s", flat, merge)
	}
}

// The migration replace must mention EVERY data column, so a re-run rebuilds the
// row rather than leaving stale values behind.
func TestReplaceUpsertTouchesEveryColumn(t *testing.T) {
	s := suiteStore()
	var stats WriteStats
	enc, err := s.encode(map[string]any{"userCards": []any{1}}, WriteMigrate, &stats)
	if err != nil {
		t.Fatal(err)
	}
	sql, args := s.upsertStatement(1, 1, enc, clearAll)
	if strings.Contains(sql, "COALESCE") {
		t.Fatalf("replace statement merges:\n%s", sql)
	}
	if want := s.cat.Len() + 4; len(args) != want {
		t.Fatalf("args = %d, want %d", len(args), want)
	}
	if !strings.Contains(sql, catalog.QuoteIdent("user_decks_j")) {
		t.Fatal("replace statement omits an unsupplied column")
	}
}

// A suite upload must hold the three history keys back: they are resolved
// against the stored side inside the transaction, not written directly.
func TestSuiteModeHoldsBackTheHistoryKeys(t *testing.T) {
	s := suiteStore()
	var stats WriteStats
	enc, err := s.encode(map[string]any{
		"userEvents": []any{map[string]any{"eventId": 1}},
		"userCards":  []any{1},
	}, WriteSuite, &stats)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := s.cat.Resolve("userEvents")
	if _, wrote := enc.columns[e.Column]; wrote {
		t.Fatal("userEvents was written directly instead of being merged")
	}
	if _, held := enc.mergedRaw["userEvents"]; !held {
		t.Fatal("userEvents was not held back for the merge")
	}
	// A non-suite mode writes it straight through.
	var s2 WriteStats
	enc2, err := s.encode(map[string]any{"userEvents": []any{map[string]any{"eventId": 1}}}, WriteMysekai, &s2)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrote := enc2.columns[e.Column]; !wrote {
		t.Fatal("non-suite mode also held userEvents back")
	}
}

// --- identity ---------------------------------------------------------------

// Identity lives in the primary key, never in a json column, or a client could
// overwrite whose row this is.
func TestIdentityFieldsNeverBecomeColumns(t *testing.T) {
	s := suiteStore()
	var stats WriteStats
	enc, err := s.encode(map[string]any{
		"_id":         999,
		"server":      "cn",
		"userCards":   []any{1},
		"upload_time": 1758686145,
	}, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	if enc.extra != nil {
		for _, banned := range []string{`"_id"`, `"server"`} {
			if strings.Contains(string(enc.extra), banned) {
				t.Fatalf("%s leaked into extra: %s", banned, enc.extra)
			}
		}
	}
	if !enc.hasUpload || *enc.uploadTime != 1758686145 {
		t.Fatalf("upload_time not captured: %v", enc.uploadTime)
	}
}

// --- extra -------------------------------------------------------------------

func TestUnknownKeysGoToExtraAndAreReported(t *testing.T) {
	s := suiteStore()
	var stats WriteStats
	enc, err := s.encode(map[string]any{"userBrandNewKey": []any{1}}, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	if enc.extra == nil {
		t.Fatal("unknown key did not reach extra")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(enc.extra, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["userBrandNewKey"]; !ok {
		t.Fatalf("extra = %s", enc.extra)
	}
	if len(stats.ExtraKeys) != 1 || stats.ExtraKeys[0] != "userBrandNewKey" {
		t.Fatalf("ExtraKeys = %v", stats.ExtraKeys)
	}
}

// Unknown children of the flattened parent must re-nest under it, not appear as
// dotted top-level keys.
func TestUnknownFlattenedChildrenRenestUnderTheParent(t *testing.T) {
	s := mysekaiStore()
	var stats WriteStats
	enc, err := s.encode(map[string]any{
		s.cat.FlattenKey: map[string]any{"userBrandNewChild": []any{1}},
	}, WriteMysekai, &stats)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(enc.extra, &m); err != nil {
		t.Fatal(err)
	}
	sub, ok := m[s.cat.FlattenKey]
	if !ok {
		t.Fatalf("unknown child did not re-nest: %s", enc.extra)
	}
	if !strings.Contains(string(sub), "userBrandNewChild") {
		t.Fatalf("child missing: %s", sub)
	}
}

// --- validation --------------------------------------------------------------

func TestFieldNameValidationRejectsOperatorsNulAndBadUTF8(t *testing.T) {
	cases := []map[string]any{
		{"a.b": 1},
		{"$set": 1},
		{"ok": map[string]any{"nested.bad": 1}},
		{"ok": []any{map[string]any{"$inc": 1}}},
		{"a\x00b": 1},
		{string([]byte{0xff, 0xfe}): 1},
		{"ok": "has\x00nul"},
		{"ok": string([]byte{0xff, 0xfe})},
	}
	for i, c := range cases {
		if err := ValidateUploadFieldNames(c); err == nil {
			t.Errorf("case %d accepted: %v", i, c)
		}
	}
	if err := ValidateUploadFieldNames(map[string]any{
		"userCards": []any{map[string]any{"cardId": 1, "名前": "ok"}},
	}); err != nil {
		t.Fatalf("legitimate payload rejected: %v", err)
	}
}

// Deep nesting must be refused rather than recursed into: a Go stack overflow is
// fatal and cannot be recovered.
func TestDeeplyNestedUploadIsRefused(t *testing.T) {
	var v any = 1
	for i := 0; i < maxValidateDepth+10; i++ {
		v = map[string]any{"a": v}
	}
	if err := ValidateUploadFieldNames(v); err == nil {
		t.Fatal("unbounded nesting accepted")
	}
}

// --- limits ------------------------------------------------------------------

// Removing MongoDB removes the only bound on attacker-influenced growth, so the
// caps have to actually fire.
func TestLimitsFire(t *testing.T) {
	l := Limits{MaxKeyBytes: 10, MaxRowBytes: 100, MaxExtraKeys: 2, MaxExtraBytes: 20}

	if err := checkLimits(l, map[string]int{"userCards": 11}, 0, 0, 11); err == nil {
		t.Fatal("per-key limit did not fire")
	}
	if err := checkLimits(l, nil, 3, 0, 0); err == nil {
		t.Fatal("extra-key-count limit did not fire")
	}
	if err := checkLimits(l, nil, 1, 21, 21); err == nil {
		t.Fatal("extra-bytes limit did not fire")
	}
	if err := checkLimits(l, nil, 0, 0, 101); err == nil {
		t.Fatal("row limit did not fire")
	}
	if err := checkLimits(l, map[string]int{"userCards": 5}, 1, 5, 5); err != nil {
		t.Fatalf("a legitimate upload was rejected: %v", err)
	}
}

// A zero Limits must not silently disable the caps for a caller that forgot to
// set them — it disables them explicitly, so the default has to be non-zero.
func TestDefaultLimitsAreAllSet(t *testing.T) {
	l := DefaultLimits()
	if l.MaxKeyBytes <= 0 || l.MaxRowBytes <= 0 || l.MaxExtraKeys <= 0 || l.MaxExtraBytes <= 0 {
		t.Fatalf("DefaultLimits leaves a cap disabled: %+v", l)
	}
	if l.MaxKeyBytes > l.MaxRowBytes {
		t.Fatal("per-key cap exceeds the whole-row cap")
	}
}

// --- number precision --------------------------------------------------------

func TestEncodeKeepsLargeIntegersExact(t *testing.T) {
	b, err := encodeJSON(map[string]any{"userId": int64(28808221489823746)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "28808221489823746") {
		t.Fatalf("large integer lost precision: %s", b)
	}
}

func TestDecodeUsesJSONNumber(t *testing.T) {
	v, err := decodeJSONNumbers([]byte(`[{"eventId":28808221489823746}]`))
	if err != nil {
		t.Fatal(err)
	}
	arr := v.([]any)
	m := arr[0].(map[string]any)
	if _, ok := m["eventId"].(json.Number); !ok {
		t.Fatalf("eventId decoded as %T, want json.Number", m["eventId"])
	}
}

// An empty server must map to the reserved parking code rather than being
// refused: the primary key is NOT NULL, so refusing would drop the row.
func TestEmptyServerIsParkedUnderTheReservedCode(t *testing.T) {
	if _, ok := catalog.ServerCode(""); ok {
		t.Fatal("the empty region resolved to a real server code")
	}
	if _, ok := catalog.ServerName(catalog.ServerUnknown); ok {
		t.Fatal("the parking code maps back to a region name")
	}
	// A row parked there must not be reachable by any real region.
	for name := range catalog.ServerCodes {
		if code, _ := catalog.ServerCode(name); code == catalog.ServerUnknown {
			t.Fatalf("region %q collides with the parking code", name)
		}
	}
}

// The parked row must be reachable by the same empty region that wrote it,
// while a genuinely unknown region stays absent. Found by running verify against
// the real corpus: it reported the parked document as MISSING.
func TestEmptyRegionAddressesTheParkedRowButUnknownRegionsDoNot(t *testing.T) {
	if _, ok := catalog.ServerCode(""); ok {
		t.Fatal("the empty region resolved to a real code")
	}
	if _, ok := catalog.ServerCode("zz"); ok {
		t.Fatal("an unknown region resolved to a real code")
	}
	// Fetch turns "" into the parking code and a bad region into ErrNoRow; both
	// paths run before any query, so a nil pool is enough to exercise the split.
	s := NewStore(&Pool{}, catalog.Suite())
	if _, err := s.Fetch(t.Context(), 1, "zz", nil); !errors.Is(err, ErrNoRow) {
		t.Fatalf("unknown region gave %v, want ErrNoRow", err)
	}
}

// A document carrying BOTH spellings of a compact key must resolve
// deterministically, with the ROW form winning — that is what production's
// GetValueFromResult does (exact key first, compact only as a fallback).
//
// Without an explicit rule the winner is decided by Go map iteration order, so
// the same document migrates differently on two runs. Found on the real corpus:
// two cn documents carry both spellings of all six compact keys.
func TestBothSpellingsResolveToTheRowFormDeterministically(t *testing.T) {
	s := suiteStore()
	rowForm := `[{"musicId":1,"playResult":"clear"}]`
	compact := `{"__ENUM__":{"playResult":["clear"]},"musicId":[1],"playResult":[0]}`

	// Run it repeatedly: map iteration order varies, so a non-deterministic
	// implementation fails this most of the time rather than never.
	for i := 0; i < 50; i++ {
		var stats WriteStats
		enc, err := s.encode(map[string]any{
			"userMusicResults":        mustJSONValue(t, rowForm),
			"compactUserMusicResults": mustJSONValue(t, compact),
		}, WriteMysekai, &stats)
		if err != nil {
			t.Fatal(err)
		}
		e, _ := s.cat.Resolve("userMusicResults")
		got := string(enc.columns[e.Column])
		var want, gotv any
		if err := json.Unmarshal([]byte(rowForm), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(got), &gotv); err != nil {
			t.Fatalf("column holds invalid JSON: %s", got)
		}
		if !jsonEqual(want, gotv) {
			t.Fatalf("iteration %d: column holds the compact form, want the row form: %s", i, got)
		}
		if stats.AliasConflicts["userMusicResults"] != 1 {
			t.Fatalf("alias conflict not counted: %v", stats.AliasConflicts)
		}
		// The loser must be preserved, not dropped.
		if enc.extra == nil || !strings.Contains(string(enc.extra), "compactUserMusicResults") {
			t.Fatalf("the losing spelling was discarded: %s", enc.extra)
		}
	}
}

func mustJSONValue(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func jsonEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ab) == string(bb)
}
