package gamedata

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

func TestCompactExpansionOwnsOutput(t *testing.T) {
	// Force the decoder to refill while keeping whitespace and escaped lexemes.
	input := []byte(` { "key\"😀" : [ "` + strings.Repeat("x", 20000) + `\u0061", 1.500 ], "nested": [{"x":[1e2]},null] } `)
	want, err := referenceExpandCompactJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(input)
	got, err := ExpandCompactJSON(input)
	if err != nil || !bytes.Equal(want, got) {
		t.Fatal("expansion changed output")
	}
	if !bytes.Equal(input, before) {
		t.Fatal("expansion mutated input")
	}
	for i := range input {
		input[i] = 'x'
	}
	if !bytes.Equal(want, got) {
		t.Fatal("expanded output retained borrowed input")
	}
}

func TestOrderedParserStillOwnsScalarBytes(t *testing.T) {
	input := []byte(`{"value":[28808221489823746,"\u0061",1.500],"nested":{"v":true}}`)
	want := bytes.Clone(input)
	parsed, err := parseOrdered(input)
	if err != nil {
		t.Fatal(err)
	}
	for i := range input {
		input[i] = 'x'
	}
	got, err := appendJSON(nil, parsed)
	if err != nil || !bytes.Equal(want, got) {
		t.Fatal("ordinary parser borrowed input")
	}
}

func TestCompactExpansionConcurrentRows(t *testing.T) {
	c := catalog.Suite()
	e, _ := c.Resolve("userMusicResults")
	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Go(func() {
			for iteration := range 16 {
				id := 28808221489823746 + int64(worker*16+iteration)
				input := []byte(fmt.Sprintf(`{"__ENUM__":{"result":["worker-%d"]},"userId":[%d],"result":[0]}`, worker, id))
				row := &Row{cat: c, byColumn: map[string][]byte{e.Column: input}}
				if !row.HasAny([]string{"compactUserMusicResults"}) {
					t.Error("missing value")
					return
				}
				got, err := row.SuiteBody([]string{"userMusicResults"}, true)
				want := fmt.Sprintf(`[{"userId":%d,"result":"worker-%d"}]`, id, worker)
				if err != nil || string(got) != want {
					t.Error("concurrent expansion crossed row boundary")
					return
				}
			}
		})
	}
	wg.Wait()
}
