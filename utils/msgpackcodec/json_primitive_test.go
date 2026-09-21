package msgpackcodec

import (
	"bytes"
	"encoding/json/jsontext"
	"math"
	"strings"
	"testing"
)

func TestWriteJSONStringEscapes(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeJSONString(jsontext.NewEncoder(&out), []byte("a\"b\\c\n\t")); err != nil {
		t.Fatalf("writeJSONString returned error: %v", err)
	}
	if strings.TrimSpace(out.String()) != `"a\"b\\c\n\t"` {
		t.Fatalf("writeJSONString output = %q", out.String())
	}
}

func TestWriteFloatSpecialValues(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeFloat(jsontext.NewEncoder(&out), math.NaN()); err != nil {
		t.Fatalf("writeFloat NaN returned error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "null" {
		t.Fatalf("writeFloat(NaN) output = %q, want %q", out.String(), "null")
	}

	out.Reset()
	if err := writeFloat(jsontext.NewEncoder(&out), math.Inf(1)); err != nil {
		t.Fatalf("writeFloat Inf returned error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "null" {
		t.Fatalf("writeFloat(Inf) output = %q, want %q", out.String(), "null")
	}
}
