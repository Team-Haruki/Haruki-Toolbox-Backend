package streamjson

import (
	"bytes"
	"math"
	"testing"
)

func TestConvertSimpleMap(t *testing.T) {
	t.Parallel()

	// {"a":1,"b":"x"}
	msgpack := []byte{
		0x82,
		0xa1, 'a',
		0x01,
		0xa1, 'b',
		0xa1, 'x',
	}
	var out bytes.Buffer
	if err := Convert(msgpack, &out); err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if out.String() != `{"a":1,"b":"x"}` {
		t.Fatalf("Convert output = %q, want %q", out.String(), `{"a":1,"b":"x"}`)
	}
}

func TestConvertSimpleArray(t *testing.T) {
	t.Parallel()

	// [1,true,null,"x"]
	msgpack := []byte{
		0x94,
		0x01,
		0xc3,
		0xc0,
		0xa1, 'x',
	}
	var out bytes.Buffer
	if err := Convert(msgpack, &out); err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if out.String() != `[1,true,null,"x"]` {
		t.Fatalf("Convert output = %q, want %q", out.String(), `[1,true,null,"x"]`)
	}
}

func TestConvertInvalidData(t *testing.T) {
	t.Parallel()

	// Truncated fixmap: key exists but value is missing.
	msgpack := []byte{0x81, 0xa1, 'a'}
	var out bytes.Buffer
	if err := Convert(msgpack, &out); err == nil {
		t.Fatalf("Convert should fail for invalid truncated msgpack")
	}
}

func TestWriteJSONStringEscapes(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeJSONString(&out, []byte("a\"b\\c\n\t")); err != nil {
		t.Fatalf("writeJSONString returned error: %v", err)
	}
	if out.String() != `"a\"b\\c\n\t"` {
		t.Fatalf("writeJSONString output = %q", out.String())
	}
}

func TestWriteFloatSpecialValues(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeFloat(&out, math.NaN()); err != nil {
		t.Fatalf("writeFloat NaN returned error: %v", err)
	}
	if out.String() != "null" {
		t.Fatalf("writeFloat(NaN) output = %q, want %q", out.String(), "null")
	}

	out.Reset()
	if err := writeFloat(&out, math.Inf(1)); err != nil {
		t.Fatalf("writeFloat Inf returned error: %v", err)
	}
	if out.String() != "null" {
		t.Fatalf("writeFloat(Inf) output = %q, want %q", out.String(), "null")
	}
}
