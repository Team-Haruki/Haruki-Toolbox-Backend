package gamedata

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

func TestDiffColumnsFindsBothDirections(t *testing.T) {
	missing, unknown := DiffColumns(
		[]string{"user_id", "server", "a_j", "b_j"},
		[]string{"user_id", "server", "b_j", "legacy_j"},
	)
	if !reflect.DeepEqual(missing, []string{"a_j"}) {
		t.Fatalf("missing = %v", missing)
	}
	if !reflect.DeepEqual(unknown, []string{"legacy_j"}) {
		t.Fatalf("unknown = %v", unknown)
	}
}

func TestDiffColumnsOnAnEmptyTable(t *testing.T) {
	missing, unknown := DiffColumns([]string{"a_j", "b_j"}, nil)
	if len(missing) != 2 || len(unknown) != 0 {
		t.Fatalf("missing=%v unknown=%v", missing, unknown)
	}
}

// A rollback to an older build leaves columns the compiled catalog no longer
// names. Those rows stay readable, so an unknown column must NOT be reported as
// missing (which would trigger an ALTER) nor treated as an error.
func TestUnknownColumnsAreNotMissing(t *testing.T) {
	missing, unknown := DiffColumns([]string{"a_j"}, []string{"a_j", "from_a_newer_build_j"})
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if !reflect.DeepEqual(unknown, []string{"from_a_newer_build_j"}) {
		t.Fatalf("unknown = %v", unknown)
	}
}

// The live catalogs must diff clean against their own DDL, or EnsureSchema would
// try to ALTER a table it just created.
func TestFreshDDLSatisfiesItsOwnCatalog(t *testing.T) {
	for _, c := range []*catalog.Catalog{catalog.Suite(), catalog.Mysekai()} {
		ddl := c.DDL(catalog.DefaultDDLOptions())
		var have []string
		for _, col := range c.AllColumns() {
			if !strings.Contains(ddl, catalog.QuoteIdent(col)) {
				t.Fatalf("%s: DDL omits %q", c.Table, col)
			}
			have = append(have, col)
		}
		missing, unknown := DiffColumns(c.AllColumns(), have)
		if len(missing) != 0 || len(unknown) != 0 {
			t.Fatalf("%s: fresh DDL diffs dirty: missing=%v unknown=%v", c.Table, missing, unknown)
		}
	}
}

// The checksum must move when the layout moves, and not otherwise — it is the
// only thing standing between a rolled-back binary and reading the wrong columns.
func TestChecksumTracksLayout(t *testing.T) {
	base := catalog.Suite().Checksum()
	if base == "" {
		t.Fatal("empty checksum")
	}
	if base != catalog.Suite().Checksum() {
		t.Fatal("checksum is not stable across calls")
	}
	if base == catalog.Mysekai().Checksum() {
		t.Fatal("two different catalogs share a checksum")
	}
}

func TestEnsureSchemaRejectsNilPool(t *testing.T) {
	if _, err := EnsureSchema(t.Context(), nil, false, catalog.Suite()); err == nil {
		t.Fatal("nil pool accepted")
	}
	if _, err := EnsureSchema(t.Context(), &Pool{}, false, catalog.Suite()); err == nil {
		t.Fatal("zero pool accepted")
	}
}
