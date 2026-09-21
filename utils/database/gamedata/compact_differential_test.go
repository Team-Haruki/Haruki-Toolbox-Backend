package gamedata

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func equalCompactImplementations(t *testing.T, input []byte) {
	t.Helper()
	want, oldErr := referenceExpandCompactJSON(input)
	got, err := ExpandCompactJSON(input)
	if (oldErr == nil) != (err == nil) || oldErr == nil && !bytes.Equal(want, got) {
		t.Fatalf("input=%q\nreference=%q err=%v\noptimized=%q err=%v", input, want, oldErr, got, err)
	}
	if oldErr != nil && oldErr.Error() != err.Error() {
		t.Fatalf("error changed: old=%v new=%v", oldErr, err)
	}
}
func TestCompactDifferentialEdges(t *testing.T) {
	cases := []string{
		``, `null`, `[broken`, ` {"a":[1]} `, `{"a":[1],"a":[2]}`, `{"a":[1],"\u0061":[2]}`,
		`{"a":[{"x":1,"x":2}]}`, `{"__ENUM__":{"x":[{"v":1,"v":2}]},"x":[0]}`,
		`{"a":[1e999999,18446744073709551616,-0,1.500,1E+02]}`,
		`{"__ENUM__":{"x":["first","second"]},"x":[0,0.0,0e0,-0,1.2,"0",true,null,[],{},-1,9223372036854775808]}`,
		`{"__ENUM__":{"x":null},"x":[0,1],"y":[3,4]}`, `{"__ENUM__":[],"x":[1]}`,
		`{"a":[1,2],"b":null}`, `{"__ENUM__":{}}`, `{"a":[]}`, `{"a": [ { "b" : [ 1.50, "\u0061" ] } ]}`,
		`{"\u0061":["\u0062","<>&","\u2028","\ud83d\ude00"]}`,
		`{"a":["\ud800"]}`, `{"a":["\udc00"]}`, `{"a":[01]}`, `{"a":[NaN]}`,
		`{"a":[1]} false`, `{"a":[1,]}`, "{\"a\":[\"\xff\"]}",
	}
	for _, input := range cases {
		t.Run(fmt.Sprintf("case_%d", len(input)), func(t *testing.T) { equalCompactImplementations(t, []byte(input)) })
	}
	for _, n := range []int{64, 127, 128, 255, 256, 257, 258, 1024} {
		for _, leaf := range []string{`0`, `[]`} {
			input := `{"a":[` + strings.Repeat("[", n) + leaf + strings.Repeat("]", n) + `]}`
			t.Run(fmt.Sprintf("depth_%d_%s", n, leaf), func(t *testing.T) { equalCompactImplementations(t, []byte(input)) })
		}
	}
}
func TestCompactDifferentialGenerated(t *testing.T) {
	rng := rand.New(rand.NewSource(73))
	atoms := []string{`null`, `true`, `false`, `1.50`, `1e2`, `-0`, `28808221489823746`, `18446744073709551616`, `"\u0061"`, `"中文😀"`, `{"z":1,"a":[2.00]}`, `[1,{"nested":false}]`, `0`, `1`, `-1`, `0.0`, `"0"`}
	for iteration := 0; iteration < 2000; iteration++ {
		var input strings.Builder
		input.WriteString(`{"__ENUM__":{"c1":["A",{"z": 1.50,"q": true}],"c3":[]}`)
		cols := 1 + rng.Intn(7)
		for c := 0; c < cols; c++ {
			fmt.Fprintf(&input, `,"c%d":[`, c)
			for row, n := 0, rng.Intn(20); row < n; row++ {
				if row > 0 {
					input.WriteByte(',')
				}
				input.WriteString(atoms[rng.Intn(len(atoms))])
			}
			input.WriteByte(']')
		}
		input.WriteByte('}')
		equalCompactImplementations(t, []byte(input.String()))
	}
}
func compactBenchmarkPayload(rows int) []byte {
	var b strings.Builder
	b.WriteString(`{"__ENUM__":{"playResult":["full_combo","clear","failed"]}`)
	cols := []string{"userId", "musicId", "playResult", "score", "accuracy", "enabled", "name", "meta"}
	for c, key := range cols {
		fmt.Fprintf(&b, `,"%s":[`, key)
		for row := 0; row < rows; row++ {
			if row > 0 {
				b.WriteByte(',')
			}
			switch c {
			case 0:
				b.WriteString("28808221489823746")
			case 1:
				fmt.Fprint(&b, row+1)
			case 2:
				fmt.Fprint(&b, row%3)
			case 3:
				fmt.Fprint(&b, 1000000+row)
			case 4:
				b.WriteString("99.500")
			case 5:
				b.WriteString("true")
			case 6:
				b.WriteString(`"テスト😀"`)
			case 7:
				b.WriteString(`{"z":1,"a":[true,null,1e2]}`)
			}
		}
		b.WriteByte(']')
	}
	b.WriteByte('}')
	return []byte(b.String())
}
func FuzzCompactExpansion(f *testing.F) {
	for _, s := range []string{`{"a":[1]}`, `{"a":["\ud800"]}`, `{"a":[1e999]}`, `{"__ENUM__":{"x":["A"]},"x":[0,1]}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 65536 {
			t.Skip()
		}
		equalCompactImplementations(t, input)
	})
}

func TestCompactBenchmarkPayloads(t *testing.T) {
	for _, n := range []int{8, 256, 8192} {
		input := compactBenchmarkPayload(n)
		equalCompactImplementations(t, input)
		out, err := ExpandCompactJSON(input)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("rows=%d input=%d output=%d", n, len(input), len(out))
	}
}
