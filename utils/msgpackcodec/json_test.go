package msgpackcodec

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
	"strconv"
	"testing"
)

func TestJSONProjectionContract(t *testing.T) {
	// Deliberately use non-provider names to keep the codec independent of games.
	rule := JSONOptions{DerivedStringField: StringFieldRule{"record", "id", "text"}}
	tests := []struct {
		name, want string
		data       []byte
		options    JSONOptions
	}{
		{"plain", `{"record":{"id":1}}`, []byte("\x81\xa6record\x81\xa2id\x01"), JSONOptions{}},
		{"derive", `{"record":{"id":1,"text":"1"}}`, []byte("\x81\xa6record\x81\xa2id\x01"), rule},
		{"override", `{"record":{"id":1,"text":"1"}}`, []byte("\x81\xa6record\x82\xa4text\xa3old\xa2id\x01"), rule},
		{"fallback", `{"record":{"id":null,"text":false}}`, []byte("\x81\xa6record\x82\xa4text\xc2\xa2id\xc0"), rule},
		{"empty_source", `{"record":{"id":"","text":false}}`, []byte("\x81\xa6record\x82\xa4text\xc2\xa2id\xa0"), rule},
		{"nested", `{"x":{"record":{"id":1,"text":"1"}}}`, []byte("\x81\xa1x\x81\xa6record\x81\xa2id\x01"), rule},
		{"array_breaks_match", `{"record":[{"id":1}]}`, []byte("\x81\xa6record\x91\x81\xa2id\x01"), rule},
		{"duplicate_source", "", []byte("\x81\xa6record\x82\xa2id\x01\xa2id\x02"), rule},
		{"duplicate_target", "", []byte("\x81\xa6record\x82\xa4text\x01\xa4text\x02"), rule},
		{"duplicate_generic", "", []byte("\x82\xa1a\x01\xa1a\x02"), JSONOptions{}},
		{"invalid_utf8", "", []byte{0xa1, 0xff}, JSONOptions{}},
		{"non_string_key", "", []byte{0x81, 1, 2}, JSONOptions{}},
		{"binary", "null", []byte{0xc4, 1, 42}, JSONOptions{}},
		{"extension", "null", []byte{0xd4, 100, 42}, JSONOptions{}},
		{"uint64", "18446744073709551615", []byte{0xcf, 255, 255, 255, 255, 255, 255, 255, 255}, JSONOptions{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := WriteJSON(&out, tc.data, tc.options)
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid JSON input accepted")
				}
				return
			}
			if err != nil || string(bytes.TrimSpace(out.Bytes())) != tc.want {
				t.Fatalf("got %q (%v), want %q", out.String(), err, tc.want)
			}
		})
	}
}

func TestJSONStructuralErrorsWriteNothing(t *testing.T) {
	// Enough valid data to force encoder flushes if the preflight is removed.
	tail := []byte{0xdc, 0x10, 0x01}
	for range 4096 {
		tail = append(tail, 0xa3, 'a', 'b', 'c')
	}
	tail = append(tail, 0xdb, 0, 1, 0, 0)
	for _, data := range [][]byte{tail, {0xc0, 0xc0}, {0xdd, 255, 255, 255, 255}, {0xdf, 255, 255, 255, 255}} {
		var out bytes.Buffer
		if err := WriteJSON(&out, data, JSONOptions{}); err == nil || out.Len() != 0 {
			t.Fatalf("malformed input: error=%v, output=%d", err, out.Len())
		}
	}
	for _, depth := range []int{255, 256, 257, 512} {
		data := append(bytes.Repeat([]byte{0x91}, depth), 0xc0)
		var out bytes.Buffer
		err := WriteJSON(&out, data, JSONOptions{})
		if (err == nil) != (depth <= DefaultMaxUploadDepth) || err != nil && out.Len() != 0 {
			t.Fatalf("depth %d: error=%v, output=%d", depth, err, out.Len())
		}
	}
}

type failingJSONWriter struct{ err error }

func (w failingJSONWriter) Write([]byte) (int, error) { return 0, w.err }

func TestJSONWriterAndOptionErrors(t *testing.T) {
	want := errors.New("sink failed")
	if err := WriteJSON(failingJSONWriter{want}, []byte{0xc0}, JSONOptions{}); !errors.Is(err, want) {
		t.Fatalf("lost writer error: %v", err)
	}
	for _, rule := range []StringFieldRule{{Source: "id"}, {"x", "id", "id"}} {
		var out bytes.Buffer
		if err := WriteJSON(&out, []byte{0x80}, JSONOptions{rule}); err == nil || out.Len() != 0 {
			t.Fatal("invalid rule accepted or wrote output")
		}
	}
}

func FuzzWriteJSON(f *testing.F) {
	for _, data := range [][]byte{{0x80}, {0x91, 1}, {0xc4, 1, 42}, {0x81, 0xa1, 'a', 0xcf, 255, 255, 255, 255, 255, 255, 255, 255}} {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		var out bytes.Buffer
		err := WriteJSON(&out, data, JSONOptions{})
		if validationErr := ValidateMaxDepth(data, DefaultMaxUploadDepth); validationErr != nil {
			if err == nil || out.Len() != 0 {
				t.Fatal("invalid structure accepted or emitted")
			}
			return
		}
		if err == nil && !jsontext.Value(bytes.TrimSpace(out.Bytes())).IsValid() {
			t.Fatal("invalid JSON emitted")
		}
	})
}

func BenchmarkWriteJSONRecords(b *testing.B) {
	data := appendMapHeader(nil, 1)
	data = appendString(data, "records")
	data = appendArrayHeader(data, 5000)
	for row := range 5000 {
		data = appendMapHeader(data, 16)
		for col := range 16 {
			data = appendString(data, "field_"+strconv.Itoa(col))
			data = appendInt64(data, int64(row+col))
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if err := WriteJSON(io.Discard, data, JSONOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
