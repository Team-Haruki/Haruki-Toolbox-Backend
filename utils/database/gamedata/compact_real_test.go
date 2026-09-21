package gamedata

import (
	"bytes"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

type compactRealTable struct {
	Name    string `json:"name"`
	Section string `json:"section"`
}

// Opt-in fixtures are prepared outside the repository from an authorized
// snapshot. Neither fixture contents nor decoded values belong in test logs.
func compactRealFixtures(t testing.TB) []compactRealTable {
	t.Helper()
	dir := os.Getenv("GAMEDATA_COMPACT_FIXTURE_DIR")
	if dir == "" {
		t.Skip("set GAMEDATA_COMPACT_FIXTURE_DIR to test real derived compact tables")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "tables.metadata.json"))
	if err != nil {
		t.Fatal("fixture metadata unavailable")
	}
	var tables []compactRealTable
	if err := json.Unmarshal(raw, &tables); err != nil {
		t.Fatal("invalid fixture metadata")
	}
	return tables
}

func compactRealFixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(os.Getenv("GAMEDATA_COMPACT_FIXTURE_DIR"), name))
	if err != nil {
		t.Fatal("fixture unavailable")
	}
	return raw
}

func TestCompactRealReadPath(t *testing.T) {
	for _, table := range compactRealFixtures(t) {
		t.Run(table.Name, func(t *testing.T) {
			input := compactRealFixture(t, table.Name+".compact.json")
			want := compactRealFixture(t, table.Name+".rows.json")
			c := catalog.Suite()
			e, place := c.Resolve(table.Section)
			if place != catalog.PlaceColumn || e.Storage != catalog.StorageCompactJSON {
				t.Fatal("fixture is not a compact column")
			}
			row := &Row{cat: c, byColumn: map[string][]byte{e.Column: input}}
			if !row.HasAny([]string{table.Section}) {
				t.Fatal("real table unexpectedly absent")
			}
			got, err := row.SuiteBody([]string{table.Section}, true)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("real read output mismatch; contents omitted")
			}
			// Exercise the alias after the presence check and bare response.
			for _, alias := range e.Aliases {
				got, err := row.PrivateBody([]string{alias})
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal("real alias output mismatch; contents omitted")
				}
			}
			t.Logf("exact match: output_bytes=%d", len(got))
		})
	}
}

func BenchmarkCompactRealReadPath(b *testing.B) {
	for _, table := range compactRealFixtures(b) {
		input := compactRealFixture(b, table.Name+".compact.json")
		c := catalog.Suite()
		e, place := c.Resolve(table.Section)
		if place != catalog.PlaceColumn {
			b.Fatal("fixture is not a catalog column")
		}
		keys := []string{table.Section}
		for _, parallel := range []bool{false, true} {
			mode := "Serial"
			if parallel {
				mode = "Parallel"
			}
			b.Run(table.Name+"/"+mode, func(b *testing.B) {
				b.ReportAllocs()
				work := func() {
					row := &Row{cat: c, byColumn: map[string][]byte{e.Column: input}}
					if !row.HasAny(keys) {
						panic("benchmark table absent")
					}
					out, err := row.SuiteBody(keys, true)
					if err != nil || len(out) == 0 {
						panic("benchmark read failed; contents omitted")
					}
				}
				if parallel {
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							work()
						}
					})
				} else {
					for b.Loop() {
						work()
					}
				}
			})
		}
	}
}
