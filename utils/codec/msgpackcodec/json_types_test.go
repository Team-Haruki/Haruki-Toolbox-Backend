package msgpackcodec

import (
	"bytes"
	"encoding/binary"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"math"
	"strings"
	"testing"
)

func be16(n uint16) []byte { return binary.BigEndian.AppendUint16(nil, n) }
func be32(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }
func be64(n uint64) []byte { return binary.BigEndian.AppendUint64(nil, n) }

func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// Every MessagePack type byte the converter accepts, in each length width,
// maps to the documented JSON value.
func TestWriteJSONEveryTypeByte(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"positive_fixint", []byte{0x7f}, `127`},
		{"negative_fixint", []byte{0xff}, `-1`},
		{"negative_fixint_min", []byte{0xe0}, `-32`},
		{"nil", []byte{0xc0}, `null`},
		{"false", []byte{0xc2}, `false`},
		{"true", []byte{0xc3}, `true`},

		{"bin8", []byte{0xc4, 2, 1, 2}, `null`},
		{"bin16", join([]byte{0xc5}, be16(3), []byte{1, 2, 3}), `null`},
		{"bin32", join([]byte{0xc6}, be32(1), []byte{9}), `null`},
		{"ext8", []byte{0xc7, 2, 7, 1, 2}, `null`},
		{"ext16", join([]byte{0xc8}, be16(1), []byte{7, 1}), `null`},
		{"ext32", join([]byte{0xc9}, be32(2), []byte{7, 1, 2}), `null`},
		{"fixext1", []byte{0xd4, 7, 1}, `null`},
		{"fixext2", []byte{0xd5, 7, 1, 2}, `null`},
		{"fixext4", join([]byte{0xd6, 7}, make([]byte, 4)), `null`},
		{"fixext8", join([]byte{0xd7, 7}, make([]byte, 8)), `null`},
		{"fixext16", join([]byte{0xd8, 7}, make([]byte, 16)), `null`},
		// A skipped payload must not desynchronise the following element.
		{"bin_then_value", join([]byte{0x92, 0xc5}, be16(2), []byte{0xc3, 0xc3, 0x05}), `[null,5]`},

		{"float32", join([]byte{0xca}, be32(math.Float32bits(1.5))), `1.5`},
		{"float32_nan", join([]byte{0xca}, be32(math.Float32bits(float32(math.NaN())))), `null`},
		{"float64", join([]byte{0xcb}, be64(math.Float64bits(-0.25))), `-0.25`},
		{"float64_inf", join([]byte{0xcb}, be64(math.Float64bits(math.Inf(1)))), `null`},

		{"uint8", []byte{0xcc, 0xff}, `255`},
		{"uint16", join([]byte{0xcd}, be16(math.MaxUint16)), `65535`},
		{"uint32", join([]byte{0xce}, be32(math.MaxUint32)), `4294967295`},
		{"uint64", join([]byte{0xcf}, be64(math.MaxUint64)), `18446744073709551615`},
		{"int8", []byte{0xd0, 0x80}, `-128`},
		{"int16", join([]byte{0xd1}, be16(0x8000)), `-32768`},
		{"int32", join([]byte{0xd2}, be32(0x80000000)), `-2147483648`},
		{"int64", join([]byte{0xd3}, be64(0x8000000000000000)), `-9223372036854775808`},
		{"int64_beyond_float", join([]byte{0xd3}, be64(9007199254740993)), `9007199254740993`},

		{"fixstr", []byte{0xa2, 'h', 'i'}, `"hi"`},
		{"str8", []byte{0xd9, 3, 'a', '"', 'b'}, `"a\"b"`},
		{"str16", join([]byte{0xda}, be16(2), []byte("ok")), `"ok"`},
		{"str32", join([]byte{0xdb}, be32(3), []byte("abc")), `"abc"`},
		{"str8_empty", []byte{0xd9, 0}, `""`},

		{"array16", join([]byte{0xdc}, be16(2), []byte{0x01, 0xc2}), `[1,false]`},
		{"array32", join([]byte{0xdd}, be32(1), []byte{0xa1, 'x'}), `["x"]`},
		{"array16_empty", join([]byte{0xdc}, be16(0)), `[]`},
		{"map16", join([]byte{0xde}, be16(1), []byte{0xa1, 'a', 0x01}), `{"a":1}`},
		{"map32", join([]byte{0xdf}, be32(1), []byte{0xa1, 'b', 0xc0}), `{"b":null}`},

		// Keys may use any string width.
		{"key_str8", []byte{0x81, 0xd9, 1, 'k', 0x01}, `{"k":1}`},
		{"key_str16", join([]byte{0x81, 0xda}, be16(2), []byte("kk"), []byte{0x02}), `{"kk":2}`},
		{"key_str32", join([]byte{0x81, 0xdb}, be32(1), []byte("q"), []byte{0x03}), `{"q":3}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := WriteJSON(&out, tc.data, JSONOptions{}); err != nil {
				t.Fatalf("WriteJSON(% x) error: %v", tc.data, err)
			}
			if got := string(bytes.TrimSpace(out.Bytes())); got != tc.want {
				t.Fatalf("WriteJSON(% x) = %s, want %s", tc.data, got, tc.want)
			}
		})
	}
}

// The derivation rule follows the object name through every map width, and
// derives from numeric sources of every width.
func TestWriteJSONDerivedFieldAcrossWidths(t *testing.T) {
	rule := JSONOptions{DerivedStringField: StringFieldRule{ObjectName: "record", Source: "id", Target: "text"}}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			"map16_record",
			join([]byte{0x81, 0xa6}, []byte("record"), []byte{0xde}, be16(1), []byte{0xa2, 'i', 'd', 0xcd}, be16(300)),
			`{"record":{"id":300,"text":"300"}}`,
		},
		{
			"map32_record_str16_key",
			join([]byte{0x81, 0xa6}, []byte("record"), []byte{0xdf}, be32(1), []byte{0xda}, be16(2), []byte("id"), []byte{0xd0, 0xfe}),
			`{"record":{"id":-2,"text":"-2"}}`,
		},
		{
			"string_source",
			join([]byte{0x81, 0xa6}, []byte("record"), []byte{0x81, 0xa2, 'i', 'd', 0xd9, 2, 'a', 'b'}),
			`{"record":{"id":"ab","text":"ab"}}`,
		},
		{
			// A supplied target with no source survives, moved to the end.
			"supplied_target_only",
			join([]byte{0x81, 0xa6}, []byte("record"), []byte{0x82, 0xa4}, []byte("text"), []byte{0xa1, 's', 0xa1, 'z', 0x01}),
			`{"record":{"z":1,"text":"s"}}`,
		},
		{
			// A non-scalar source cannot be derived; the supplied target stays.
			"array_source_keeps_supplied",
			join([]byte{0x81, 0xa6}, []byte("record"), []byte{0x82, 0xa2, 'i', 'd', 0x91, 0x01, 0xa4}, []byte("text"), []byte{0xa1, 's'}),
			`{"record":{"id":[1],"text":"s"}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := WriteJSON(&out, tc.data, rule); err != nil {
				t.Fatalf("WriteJSON error: %v", err)
			}
			if got := string(bytes.TrimSpace(out.Bytes())); got != tc.want {
				t.Fatalf("WriteJSON = %s, want %s", got, tc.want)
			}
		})
	}
}

// A rule whose target is not valid UTF-8 cannot be emitted; the converter
// must fail instead of writing a corrupt object name.
func TestWriteJSONRejectsUnencodableTarget(t *testing.T) {
	rule := JSONOptions{DerivedStringField: StringFieldRule{ObjectName: "record", Source: "id", Target: "\xff"}}
	for name, data := range map[string][]byte{
		"derived":  join([]byte{0x81, 0xa6}, []byte("record"), []byte{0x81, 0xa2, 'i', 'd', 0x01}),
		"supplied": join([]byte{0x81, 0xa6}, []byte("record"), []byte{0x81, 0xa1, 0xff, 0x01}),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := WriteJSON(&out, data, rule); err == nil {
				t.Fatalf("WriteJSON accepted an invalid UTF-8 target, wrote %q", out.String())
			}
		})
	}
}

func TestWriteJSONRejectsTrailingAndUnsupportedBytes(t *testing.T) {
	for name, data := range map[string][]byte{
		"trailing":    {0x01, 0x02},
		"unsupported": {0xc1},
		"empty":       {},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := WriteJSON(&out, data, JSONOptions{}); err == nil {
				t.Fatalf("WriteJSON(% x) accepted, wrote %q", data, out.String())
			}
		})
	}
}

// writeValue is reachable only after ValidateMaxDepth today, but it must still
// fail cleanly (never panic or read past the input) on truncated or malformed
// input, so a future caller that skips the preflight stays memory safe.
func TestWriteValueWithoutPreflightFailsCleanly(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"empty", nil, "read type byte"},
		{"unsupported", []byte{0xc1}, "unsupported msgpack type byte: 0xc1"},
		{"str8_too_long", []byte{0xd9, 5, 'a'}, "string length 5 exceeds remaining bytes 1"},
		{"str32_too_long", join([]byte{0xdb}, be32(math.MaxUint32)), "exceeds remaining bytes"},
		{"bin8_payload", []byte{0xc4, 4, 1}, "unexpected EOF"},
		{"fixext4_payload", []byte{0xd6, 7, 1}, "unexpected EOF"},
		{"array_element", []byte{0x92, 0x01}, "array element 1"},
		{"map_key_missing", []byte{0x81}, "map key 0"},
		{"map_key_non_string", []byte{0x81, 0x01, 0x01}, "non-string map key type: 0x01"},
		{"map_key_str8_header", []byte{0x81, 0xd9}, "map key 0"},
		{"map_key_str16_header", []byte{0x81, 0xda, 0}, "map key 0"},
		{"map_key_str32_header", []byte{0x81, 0xdb, 0, 0}, "map key 0"},
		{"map_key_too_long", []byte{0x81, 0xa3, 'a'}, "string length 3 exceeds remaining bytes 1"},
		{"map_value_missing", []byte{0x81, 0xa1, 'a'}, "read type byte"},
	}
	// Each length-prefixed type with a missing or short length field.
	for _, b := range []byte{
		0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf,
		0xd0, 0xd1, 0xd2, 0xd3, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf,
	} {
		tests = append(tests, struct {
			name    string
			data    []byte
			wantErr string
		}{"truncated_header", []byte{b}, "unexpected EOF"})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &jsonReader{cursor: cursor{data: tc.data}}
			var out bytes.Buffer
			err := writeValue(r, jsontext.NewEncoder(&out))
			if err == nil {
				t.Fatalf("writeValue(% x) succeeded", tc.data)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("writeValue(% x) error = %q, want it to contain %q", tc.data, err, tc.wantErr)
			}
		})
	}
}

// The document marshaler itself refuses trailing bytes, independent of the
// WriteJSON preflight.
func TestMsgpackDocumentRejectsTrailingBytes(t *testing.T) {
	doc := msgpackDocument{r: &jsonReader{cursor: cursor{data: []byte{0x01, 0x02}}}}
	if out, err := json.Marshal(doc); err == nil || !strings.Contains(err.Error(), "trailing MessagePack bytes") {
		t.Fatalf("json.Marshal = %q, %v; want a trailing-bytes error", out, err)
	}
	doc = msgpackDocument{r: &jsonReader{cursor: cursor{data: []byte{0x01}}}}
	if out, err := json.Marshal(doc); err != nil || string(out) != "1" {
		t.Fatalf("json.Marshal = %q, %v; want 1", out, err)
	}
}

// With the derivation rule active, a truncated source or supplied target is
// reported instead of being dropped.
func TestWriteValueRuleBranchesPropagateErrors(t *testing.T) {
	rule := StringFieldRule{ObjectName: "record", Source: "id", Target: "text"}
	for name, data := range map[string][]byte{
		"source":   join([]byte{0x81, 0xa6}, []byte("record"), []byte{0x81, 0xa2, 'i', 'd'}),
		"supplied": join([]byte{0x81, 0xa6}, []byte("record"), []byte{0x81, 0xa4}, []byte("text"), []byte{0xd9}),
	} {
		t.Run(name, func(t *testing.T) {
			r := &jsonReader{cursor: cursor{data: data}, rule: rule}
			var out bytes.Buffer
			if err := writeValue(r, jsontext.NewEncoder(&out)); err == nil {
				t.Fatalf("writeValue(% x) succeeded", data)
			}
		})
	}
}

// Containers must start where the encoder accepts a value; in an object-name
// position the encoder's syntax error is returned rather than ignored.
func TestCompoundWritersReturnEncoderSyntaxErrors(t *testing.T) {
	newNamePositionEncoder := func(t *testing.T) *jsontext.Encoder {
		t.Helper()
		enc := jsontext.NewEncoder(&bytes.Buffer{})
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			t.Fatal(err)
		}
		return enc
	}
	if err := writeMap(&jsonReader{cursor: cursor{data: nil}}, newNamePositionEncoder(t), 0, ""); err == nil {
		t.Fatal("writeMap started an object in a name position")
	}
	if err := writeArray(&jsonReader{cursor: cursor{data: nil}}, newNamePositionEncoder(t), 0); err == nil {
		t.Fatal("writeArray started an array in a name position")
	}
}

func TestCursorBounds(t *testing.T) {
	c := cursor{data: []byte{1, 2, 3}}
	if err := c.unreadByte(); err == nil || !strings.Contains(err.Error(), "unread at start") {
		t.Fatalf("unreadByte at start = %v", err)
	}
	if _, err := c.readBytesCopy(-1); err == nil || !strings.Contains(err.Error(), "negative length -1") {
		t.Fatalf("readBytesCopy(-1) = %v", err)
	}
	got, err := c.readBytesCopy(2)
	if err != nil || !bytes.Equal(got, []byte{1, 2}) {
		t.Fatalf("readBytesCopy(2) = %v, %v", got, err)
	}
	// The copy must not alias the input.
	got[0] = 9
	if c.data[0] != 1 {
		t.Fatal("readBytesCopy aliased the input")
	}
	if _, err := c.readBytesCopy(2); err == nil || c.off != 2 {
		t.Fatalf("over-read: err=%v off=%d, want error and unchanged offset", err, c.off)
	}
	for _, read := range []func() error{
		func() error { _, err := c.readUint16(); return err },
		func() error { _, err := c.readUint32(); return err },
		func() error { _, err := c.readUint64(); return err },
		func() error { _, err := c.take(2); return err },
	} {
		if err := read(); err == nil || c.off != 2 {
			t.Fatalf("short read: err=%v off=%d, want error and unchanged offset", err, c.off)
		}
	}
	if b, err := c.readByte(); err != nil || b != 3 {
		t.Fatalf("readByte = %d, %v", b, err)
	}
	if _, err := c.readByte(); err == nil || !strings.Contains(err.Error(), "unexpected EOF at offset 3") {
		t.Fatalf("readByte at end = %v", err)
	}
	if err := c.unreadByte(); err != nil || c.remaining() != 1 {
		t.Fatalf("unreadByte = %v, remaining %d", err, c.remaining())
	}
}
