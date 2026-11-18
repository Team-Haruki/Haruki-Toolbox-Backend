package gamedata

import (
	"bytes"
	"context"
	"errors"
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
	out := compactJSONOutput{body: make([]byte, 0), limit: DefaultLimits().MaxKeyBytes}
	out.value(parsed)
	if out.err != nil || !bytes.Equal(want, out.body) {
		t.Fatal("ordinary parser borrowed input")
	}
}

func TestCompactJSONExpansionHonorsByteBudget(t *testing.T) {
	raw := []byte(`{"id":[1,2]}`)
	want := []byte(`[{"id":1},{"id":2}]`)
	out := compactJSONOutput{body: make([]byte, 0), limit: len(want)}
	if err := renderCompactJSON(raw, &out); err != nil || !bytes.Equal(out.body, want) {
		t.Fatalf("renderCompactJSON = %s, %v", out.body, err)
	}
	sizing := compactJSONOutput{limit: len(want) - 1}
	if err := renderCompactJSON(raw, &sizing); !errors.Is(err, ErrPublishedResponseTooLarge) || sizing.size > sizing.limit || sizing.body != nil {
		t.Fatalf("sizing did not enforce its budget: size=%d, err=%v", sizing.size, err)
	}
}

func TestCompactExpansionHonorsRemainingPrivateRowBudget(t *testing.T) {
	entry := catalog.Entry{Column: "compact_fixture", Storage: catalog.StorageCompactJSON}
	raw := []byte(`{"id":[1,2]}`)
	row := &Row{byColumn: map[string][]byte{entry.Column: raw}, expandedBytes: maxPrivateResponseBytes - 8}
	if raw, ok, err := row.columnValue(&entry); err == nil || ok || raw != nil {
		t.Fatal("expanded column exceeded the row's remaining budget")
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

func TestWriteChecksExpandedSizeBeforeStorage(t *testing.T) {
	store := NewStore(&Pool{}, catalog.Suite())
	data := map[string]any{"compactUserMusicResults": map[string]any{"musicId": []int{1, 2, 3}}}
	for _, tc := range []struct {
		name   string
		limits Limits
		want   string
	}{
		{"expanded column", Limits{MaxKeyBytes: 36}, `compact key "userMusicResults"`},
		{"expanded row", Limits{MaxRowBytes: 36}, "upload totals"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Write(context.Background(), 1, "jp", data, WriteSuite, tc.limits)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Write error = %v, want %q", err, tc.want)
			}
			_, dryRunErr := store.EncodeOnly(data, WriteMigrate, tc.limits)
			if dryRunErr == nil || dryRunErr.Error() != err.Error() {
				t.Fatalf("migration dry run error = %v, want %v", dryRunErr, err)
			}
		})
	}
}
