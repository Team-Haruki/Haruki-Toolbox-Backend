package gamedata

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

func TestCompactPresenceAndAliasesReuseExpansion(t *testing.T) {
	const compact = `{"__ENUM__":{"playResult":["clear"]},"musicId":[28808221489823746],"playResult":[0]}`
	const want = `[{"musicId":28808221489823746,"playResult":"clear"}]`
	r := newTestRow(t, catalog.Suite(), map[string]string{"userMusicResults": compact}, `{"compactUserMusicResults":{"audit":true}}`)
	if !r.HasAny([]string{"compactUserMusicResults"}) {
		t.Fatal("compact key absent")
	}
	first, ok, err := r.RawValue("userMusicResults")
	if err != nil || !ok || string(first) != want {
		t.Fatalf("value = %s, %v, %v", first, ok, err)
	}
	for _, key := range []string{"userMusicResults", "compactUserMusicResults"} {
		body, err := r.SuiteBody([]string{key}, true)
		if err != nil || string(body) != want {
			t.Fatalf("%s body = %s, %v", key, body, err)
		}
		if &body[0] != &first[0] {
			t.Fatal("alias/presence path repeated expansion")
		}
	}
	if raw, ok := r.ExtraValue("compactUserMusicResults"); !ok || string(raw) != `{"audit":true}` {
		t.Fatalf("parked alias changed: %s", raw)
	}
	e, _ := r.cat.Resolve("userMusicResults")
	if string(r.byColumn[e.Column]) != compact {
		t.Fatal("stored compact bytes changed")
	}
	other := newTestRow(t, catalog.Suite(), map[string]string{"userMusicResults": `{"musicId":[2]}`}, "")
	got, err := other.SuiteBody([]string{"userMusicResults"}, true)
	if err != nil || string(got) != `[{"musicId":2}]` || string(first) != want {
		t.Fatal("expansion escaped row scope")
	}
}

func TestCompactPresenceKeepsErrorsAndEmptyValues(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{{`null`, `null`}, {`[]`, `[]`}, {`{}`, `[]`}, {` [ {"musicId":1} ] `, ` [ {"musicId":1} ] `}} {
		r := newTestRow(t, catalog.Suite(), map[string]string{"userMusicResults": tc.stored}, "")
		if !r.HasAny([]string{"userMusicResults"}) {
			t.Fatalf("present value %s marked missing", tc.stored)
		}
		got, err := r.SuiteBody([]string{"compactUserMusicResults"}, true)
		if err != nil || string(got) != tc.want {
			t.Fatalf("body %s, %v", got, err)
		}
	}
	r := newTestRow(t, catalog.Suite(), map[string]string{"userMusicResults": `{"musicId":[`, "userCards": `[]`}, "")
	for range 2 {
		if r.HasAny([]string{"userMusicResults"}) {
			t.Fatal("corrupt-only projection must stay absent")
		}
		keys := []string{"compactUserMusicResults", "userCards"}
		if !r.HasAny(keys) {
			t.Fatal("healthy sibling must remain present")
		}
		if _, err := r.SuiteBody(keys, false); err == nil {
			t.Fatal("rendering must preserve corrupt-value error")
		}
	}
}

func TestFlattenedPresenceReusesParentAndKeepsUnknownChildren(t *testing.T) {
	c := catalog.Mysekai()
	r := newTestRow(t, c, map[string]string{"updatedResources.userMysekaiPhotos": `[{"id":28808221489823746}]`}, `{"updatedResources":{"futureChild":null}}`)
	if !r.HasAny([]string{c.FlattenKey}) {
		t.Fatal("parent absent")
	}
	first, _, err := r.RawValue(c.FlattenKey)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := r.RawValue(c.FlattenKey)
	if err != nil || &first[0] != &second[0] {
		t.Fatal("parent rebuilt")
	}
	for range 2 {
		body, err := r.MysekaiBody([]string{c.FlattenKey})
		var got map[string]map[string]jsontext.Value
		if err != nil {
			t.Fatal(err)
		}
		// Decode raw child bytes to avoid float conversion of identifiers.
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if len(got[c.FlattenKey]) != 2 {
			t.Fatalf("unexpected parent: %s", body)
		}
		if !bytes.Contains(body, []byte(`28808221489823746`)) || !bytes.Contains(body, []byte(`"futureChild":null`)) {
			t.Fatalf("parent lost child values: %s", body)
		}
	}
	for _, extra := range []string{"", `{"updatedResources":{}}`, `{"updatedResources":null}`, `{"updatedResources":"invalid"}`} {
		empty := newTestRow(t, c, nil, extra)
		for range 2 {
			if empty.HasAny([]string{c.FlattenKey}) {
				t.Fatalf("empty parent became present: %s", extra)
			}
			body, err := empty.MysekaiBody([]string{c.FlattenKey})
			if err != nil || string(body) != `{}` {
				t.Fatalf("empty parent = %s, %v", body, err)
			}
		}
	}
}

var benchmarkRowBody []byte

// Includes the public request's presence check and render on a fresh Row.
func BenchmarkCompactPresenceAndBody(b *testing.B) {
	ids := make([]int, 4096)
	plays := make([]int, 4096)
	for i := range ids {
		ids[i] = i + 1
	}
	compact, err := json.Marshal(map[string]any{"__ENUM__": map[string]any{"playResult": []string{"clear"}}, "musicId": ids, "playResult": plays})
	if err != nil {
		b.Fatal(err)
	}
	c := catalog.Suite()
	e, _ := c.Resolve("userMusicResults")
	keys := []string{"userMusicResults"}
	b.ReportAllocs()
	for b.Loop() {
		r := &Row{cat: c, byColumn: map[string][]byte{e.Column: compact}}
		if !r.HasAny(keys) {
			b.Fatal("absent")
		}
		benchmarkRowBody, err = r.SuiteBody(keys, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFlattenedPresenceAndBody(b *testing.B) {
	c := catalog.Mysekai()
	e, _ := c.Resolve("updatedResources.userMysekaiPhotos")
	raw := bytes.Repeat([]byte(`{"id":28808221489823746},`), 4096)
	raw = append(append([]byte{'['}, raw[:len(raw)-1]...), ']')
	keys := []string{c.FlattenKey}
	b.ReportAllocs()
	for b.Loop() {
		r := &Row{cat: c, byColumn: map[string][]byte{e.Column: raw}}
		if !r.HasAny(keys) {
			b.Fatal("absent")
		}
		var err error
		benchmarkRowBody, err = r.MysekaiBody(keys)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRowFormPrivateBody(b *testing.B) {
	c := catalog.Suite()
	e, _ := c.Resolve("userMusicResults")
	raw := []byte(` [{"musicId":28808221489823746,"playResult":"clear"}] `)
	keys := []string{"userMusicResults"}
	b.ReportAllocs()
	for b.Loop() {
		r := &Row{cat: c, byColumn: map[string][]byte{e.Column: raw}}
		var err error
		benchmarkRowBody, err = r.PrivateBody(keys)
		if err != nil {
			b.Fatal(err)
		}
	}
}
