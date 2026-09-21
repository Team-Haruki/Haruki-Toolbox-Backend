package codegen

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestRewriteJSONPreservesOmissionContracts(t *testing.T) {
	source := "package p\nimport \"encoding/json\"\ntype Payload struct { Flag *bool `json:\"flag,omitempty\" yaml:\"flag,omitempty\"`; Count int `json:\"count,omitempty\"`; Items []int `json:\"items,omitempty\"` }"
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "fixture.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	RewriteJSON(file)
	var out bytes.Buffer
	if err := format.Node(&out, fs, file); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`json "encoding/json/v2"`, `json:"flag,omitzero" yaml:"flag,omitempty"`, `json:"count,omitzero"`, `json:"items,omitempty"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
	first := out.String()
	out.Reset()
	RewriteJSON(file)
	if err := format.Node(&out, fs, file); err != nil {
		t.Fatal(err)
	}
	if first != out.String() {
		t.Fatal("rewrite is not idempotent")
	}
}
