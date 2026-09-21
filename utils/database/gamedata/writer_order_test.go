package gamedata

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// Column insertion order also varies after history merges. Exercise explicit
// permutations rather than relying on randomized Go map iteration in a test.
func TestUpsertCanonicalOrderPreservesValuesAndScopes(t *testing.T) {
	stores := []struct {
		name string
		s    *Store
		keys []string
	}{
		{"suite", suiteStore(), []string{"userCards", "userDecks", "userEvents"}},
		{"mysekai", mysekaiStore(), []string{"isEnabled", "policy", "updatedResources.userMysekaiPhotos"}},
	}
	scopes := []struct {
		name  string
		scope clearScope
	}{
		{"merge", clearNone},
		{"flattened", clearFlattened},
		{"replace", clearAll},
	}
	permutations := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, store := range stores {
		for _, scope := range scopes {
			t.Run(store.name+"/"+scope.name, func(t *testing.T) {
				var baseline string
				var baselineArgs []any
				for i, permutation := range permutations {
					stamp := int64(1788711433)
					enc := &encoded{
						columns:    make(map[string][]byte),
						uploadTime: &stamp,
						hasUpload:  true,
						extra:      []byte(`{"unknown":[7]}`),
					}
					for _, index := range permutation {
						entry, place := store.s.cat.Resolve(store.keys[index])
						if place != catalog.PlaceColumn {
							t.Fatalf("fixture key %s has no column", store.keys[index])
						}
						enc.setColumn(entry.Column, []byte(fmt.Sprintf(`[{"marker":%d}]`, index+1)))
					}
					originalOrder := slices.Clone(enc.order)
					sql, args := store.s.upsertStatement(28808221489823746, 5, enc, scope.scope)
					if !slices.Equal(enc.order, originalOrder) {
						t.Fatal("generating SQL mutated the encoded column order")
					}
					assertUpsertColumnArguments(t, store.s, sql, args, enc, scope.scope)
					if i == 0 {
						baseline, baselineArgs = sql, args
					} else if sql != baseline || !reflect.DeepEqual(args, baselineArgs) {
						t.Fatalf("column permutation %v changed SQL or argument order", permutation)
					}
				}
			})
		}
	}
}

// Canonicalization must retain the set of columns a partial write owns. A full
// replacement has a fixed template, but its missing-column arguments stay NULL.
func TestUpsertCanonicalOrderRetainsDifferentFieldSets(t *testing.T) {
	s := suiteStore()
	var previousMerge string
	var replacement string
	for _, keys := range [][]string{nil, {"userCards"}, {"userCards", "userDecks"}} {
		enc := &encoded{columns: make(map[string][]byte)}
		for i, key := range keys {
			entry, _ := s.cat.Resolve(key)
			enc.setColumn(entry.Column, []byte(fmt.Sprintf("[%d]", i+1)))
		}
		merge, mergeArgs := s.upsertStatement(28808221489823746, 5, enc, clearNone)
		assertUpsertColumnArguments(t, s, merge, mergeArgs, enc, clearNone)
		if merge == previousMerge {
			t.Fatalf("different partial field sets generated the same SQL: %v", keys)
		}
		previousMerge = merge
		replace, replaceArgs := s.upsertStatement(28808221489823746, 5, enc, clearAll)
		assertUpsertColumnArguments(t, s, replace, replaceArgs, enc, clearAll)
		if replacement != "" && replace != replacement {
			t.Fatalf("full replacement template changed for field set %v", keys)
		}
		replacement = replace
	}
}

func assertUpsertColumnArguments(t *testing.T, s *Store, sql string, args []any, enc *encoded, scope clearScope) {
	t.Helper()
	list, ok := strings.CutPrefix(sql, "INSERT INTO "+catalog.QuoteIdent(s.cat.Table)+" (")
	if !ok {
		t.Fatalf("unexpected upsert: %s", sql)
	}
	list, rest, ok := strings.Cut(list, ") VALUES (")
	if !ok {
		t.Fatal("upsert has no VALUES clause")
	}
	placeholders, _, ok := strings.Cut(rest, ") ON CONFLICT (")
	if !ok {
		t.Fatal("upsert has no conflict clause")
	}
	columns := strings.Split(list, ", ")
	parameters := strings.Split(placeholders, ", ")
	if len(columns) != len(args) || len(columns) != len(parameters) {
		t.Fatalf("column/argument/placeholder counts differ: %d/%d/%d", len(columns), len(args), len(parameters))
	}
	want := map[string]any{
		catalog.ColUserID:     int64(28808221489823746),
		catalog.ColServer:     int16(5),
		catalog.ColUploadTime: nil,
		catalog.ExtraColumn:   nil,
	}
	if enc.hasUpload {
		want[catalog.ColUploadTime] = *enc.uploadTime
	}
	if enc.extra != nil {
		want[catalog.ExtraColumn] = enc.extra
	}
	hardAssign := make(map[string]bool)
	if scope == clearAll {
		hardAssign[catalog.ColUploadTime] = true
		hardAssign[catalog.ExtraColumn] = true
	}
	for _, entry := range s.cat.Entries {
		if scope == clearAll || scope == clearFlattened && entry.Path != "" {
			want[entry.Column] = nil
			hardAssign[entry.Column] = true
			if entry.Path != "" {
				hardAssign[catalog.ExtraColumn] = true
			}
		}
	}
	for column, value := range enc.columns {
		want[column] = value
	}
	if len(columns) != len(want) {
		t.Fatalf("written column count = %d, want %d", len(columns), len(want))
	}
	for i, quoted := range columns {
		column := strings.Trim(quoted, `"`)
		value, exists := want[column]
		if !exists {
			t.Fatalf("unexpected or duplicate column %s", column)
		}
		delete(want, column)
		if raw, ok := value.([]byte); ok {
			got, ok := args[i].([]byte)
			if !ok || !bytes.Equal(raw, got) {
				t.Fatalf("column %s has argument %v, want %s", column, args[i], raw)
			}
		} else if !reflect.DeepEqual(value, args[i]) {
			t.Fatalf("column %s has argument %v, want %v", column, args[i], value)
		}
		if parameters[i] != fmt.Sprintf("$%d", i+1) {
			t.Fatalf("column %s has wrong parameter %s", column, parameters[i])
		}
		if i < 2 {
			continue
		}
		assignment := quoted + " = COALESCE(EXCLUDED." + quoted + ", " + catalog.QuoteIdent(s.cat.Table) + "." + quoted + ")"
		if hardAssign[column] {
			assignment = quoted + " = EXCLUDED." + quoted
		}
		if !strings.Contains(sql, assignment) {
			t.Fatalf("column %s lost its write scope: want %s", column, assignment)
		}
	}
}
