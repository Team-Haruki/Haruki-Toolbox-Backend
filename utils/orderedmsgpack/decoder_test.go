package orderedmsgpack

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v3"
)

func TestMsgpackToOrderedMapRawExtPayloadBytes(t *testing.T) {
	data := appendMapHeader(nil, 1)
	data = appendString(data, "ext")
	data = append(data, msgpackExt8, 0x02, 0x01, 0xaa, 0xbb)

	got, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	val, exists := got.Get("ext")
	if !exists {
		t.Fatalf("ext key missing")
	}
	b, ok := val.([]byte)
	if !ok {
		t.Fatalf("decoded ext type = %T, want []byte", val)
	}
	if len(b) != 2 || b[0] != 0xaa || b[1] != 0xbb {
		t.Fatalf("decoded ext bytes = %x, want aabb", b)
	}
}

func TestMsgpackToOrderedMapFloat32(t *testing.T) {
	data := appendMapHeader(nil, 1)
	data = appendString(data, "f")
	data = append(data, msgpackFloat32, 0x3f, 0x80, 0x00, 0x00)

	got, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	val, _ := got.Get("f")
	f, ok := val.(float64)
	if !ok {
		t.Fatalf("decoded float32 type = %T, want float64", val)
	}
	if math.Abs(f-1.0) > 1e-9 {
		t.Fatalf("decoded float32 = %v, want 1.0", f)
	}
}

func TestMsgpackToOrderedMapUnsupportedType(t *testing.T) {
	data := appendMapHeader(nil, 1)
	data = appendString(data, "bad")
	data = append(data, 0xc1)

	if _, err := MsgpackToOrderedMap(data); err == nil {
		t.Fatalf("MsgpackToOrderedMap should fail for unsupported type byte")
	}
}

func TestMsgpackToOrderedMapRejectsTrailingBytes(t *testing.T) {
	data := []byte{
		msgpackFixMapMin | 0x01,
		msgpackFixStrMin | 0x01, 'a',
		0x01,
		0xc0,
	}
	if _, err := MsgpackToOrderedMap(data); err == nil {
		t.Fatalf("MsgpackToOrderedMap should reject trailing bytes")
	}
}

func TestMsgpackToOrderedMapRequiresTopLevelMap(t *testing.T) {
	if _, err := MsgpackToOrderedMap([]byte{msgpackFixArrMin | 0x01, 0x01}); err == nil {
		t.Fatalf("MsgpackToOrderedMap should fail for a non-map top-level value")
	}
}

func TestMsgpackToOrderedMapNonStringMapKeyStringified(t *testing.T) {
	data := []byte{
		msgpackFixMapMin | 0x01,
		0x01,
		msgpackFixStrMin | 0x01, 'x',
	}
	got, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	v, exists := got.Get("1")
	if !exists || v != "x" {
		t.Fatalf("decoded map value = %v (exists=%v), want x", v, exists)
	}
}

func TestMsgpackToOrderedMapDepthLimit(t *testing.T) {
	data := make([]byte, 0, maxDecodeDepth+3)
	for range maxDecodeDepth + 2 {
		data = append(data, msgpackFixArrMin|0x01)
	}
	data = append(data, 0x01)

	if _, err := MsgpackToOrderedMap(append([]byte{msgpackFixMapMin | 0x01, msgpackFixStrMin | 0x01, 'a'}, data...)); err == nil {
		t.Fatalf("MsgpackToOrderedMap should fail when max depth is exceeded")
	}
}

func TestMsgpackToOrderedMapLargeFormats(t *testing.T) {
	key := strings.Repeat("k", 300)
	data := []byte{
		msgpackMap16, 0x00, 0x01,
		msgpackStr16, 0x01, 0x2c,
	}
	data = append(data, key...)
	data = append(data,
		msgpackArray16, 0x00, 0x02,
		msgpackUint64, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfc, 0xd8,
		msgpackStr8, 0x02, 'o', 'k',
	)

	om, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	val, ok := om.Get(key)
	if !ok {
		t.Fatalf("large key missing")
	}
	arr, ok := val.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("array = %#v", val)
	}
	if arr[0] != uint64(9223372036854775000) {
		t.Fatalf("uint64 value = %v", arr[0])
	}
	if arr[1] != "ok" {
		t.Fatalf("str8 value = %v", arr[1])
	}
}

func TestOrderedMapExtLargePayloadRoundTrip(t *testing.T) {
	// The custom OrderedMap ext is an internal round-trip format, not the
	// default wire shape expected from game msgpack payloads.
	if err := RegisterOrderedMapExt(); err != nil {
		t.Fatalf("RegisterOrderedMapExt returned error: %v", err)
	}

	om := orderedmap.New()
	for i := range 80 {
		om.Set(fmt.Sprintf("key-%03d-%s", i, strings.Repeat("x", 16)), strings.Repeat("value", 8))
	}
	data, err := msgpack.Marshal(om)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if len(data) < 3 || data[0] != msgpackExt16 {
		t.Fatalf("encoded ext prefix = %x, want ext16 for payload >255 bytes", data[:min(len(data), 3)])
	}
	loaded, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	if len(loaded.Keys()) != len(om.Keys()) {
		t.Fatalf("loaded keys = %d, want %d", len(loaded.Keys()), len(om.Keys()))
	}

	var decoded orderedmap.OrderedMap
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("standard msgpack.Unmarshal returned error: %v", err)
	}
	if len(decoded.Keys()) != len(om.Keys()) {
		t.Fatalf("standard decoded keys = %d, want %d", len(decoded.Keys()), len(om.Keys()))
	}
}

func TestMsgpackToOrderedMapBinAndStringFormats(t *testing.T) {
	data := appendMapHeader(nil, 6)
	data = appendString(data, "bin8")
	data = append(data, msgpackBin8, 0x02, 0xaa, 0xbb)
	data = appendString(data, "bin16")
	data = append(data, msgpackBin16, 0x00, 0x02, 0xcc, 0xdd)
	data = appendString(data, "bin32")
	data = append(data, msgpackBin32, 0x00, 0x00, 0x00, 0x02, 0xee, 0xff)
	data = appendString(data, "str8")
	data = append(data, msgpackStr8, 0x02, 'o', 'k')
	data = appendString(data, "str16")
	data = append(data, msgpackStr16, 0x00, 0x02, 'h', 'i')
	data = appendString(data, "str32")
	data = append(data, msgpackStr32, 0x00, 0x00, 0x00, 0x02, 'g', 'o')

	om, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	for _, key := range []string{"bin8", "bin16", "bin32"} {
		if _, ok := valueOf(t, om, key).([]byte); !ok {
			t.Fatalf("%s type = %T, want []byte", key, valueOf(t, om, key))
		}
	}
	for _, key := range []string{"str8", "str16", "str32"} {
		if _, ok := valueOf(t, om, key).(string); !ok {
			t.Fatalf("%s type = %T, want string", key, valueOf(t, om, key))
		}
	}
}

func TestMsgpackToOrderedMapIntegerBoundaries(t *testing.T) {
	data := appendMapHeader(nil, 7)
	data = appendString(data, "fixpos")
	data = append(data, 0x7f)
	data = appendString(data, "fixneg")
	data = append(data, 0xe0)
	data = appendString(data, "uint8")
	data = append(data, msgpackUint8, 0xff)
	data = appendString(data, "uint16")
	data = append(data, msgpackUint16, 0xff, 0xff)
	data = appendString(data, "uint32")
	data = append(data, msgpackUint32, 0xff, 0xff, 0xff, 0xff)
	data = appendString(data, "uint64")
	data = append(data, msgpackUint64, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	data = appendString(data, "int64")
	data = append(data, msgpackInt64, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)

	om, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	want := map[string]any{
		"fixpos": int64(127),
		"fixneg": int64(-32),
		"uint8":  int64(255),
		"uint16": int64(65535),
		"uint32": int64(4294967295),
		"uint64": uint64(math.MaxUint64),
		"int64":  int64(-1),
	}
	for key, expected := range want {
		if got := valueOf(t, om, key); got != expected {
			t.Fatalf("%s = %v (%T), want %v (%T)", key, got, got, expected, expected)
		}
	}
}

func TestMsgpackToOrderedMapNestedMapArrayOrder(t *testing.T) {
	data := appendMapHeader(nil, 3)
	data = appendString(data, "first")
	data = append(data, 0x01)
	data = appendString(data, "nested")
	data = appendMapHeader(data, 2)
	data = appendString(data, "b")
	data = append(data, 0x02)
	data = appendString(data, "a")
	data = appendArrayHeader(data, 2)
	data = append(data, 0x03, 0x04)
	data = appendString(data, "last")
	data = append(data, 0x05)

	om, err := MsgpackToOrderedMap(data)
	if err != nil {
		t.Fatalf("MsgpackToOrderedMap returned error: %v", err)
	}
	if got := om.Keys(); !equalStrings(got, []string{"first", "nested", "last"}) {
		t.Fatalf("top-level keys = %v", got)
	}
	nested, ok := valueOf(t, om, "nested").(*orderedmap.OrderedMap)
	if !ok {
		t.Fatalf("nested type = %T, want *orderedmap.OrderedMap", valueOf(t, om, "nested"))
	}
	if got := nested.Keys(); !equalStrings(got, []string{"b", "a"}) {
		t.Fatalf("nested keys = %v", got)
	}
}

func TestMsgpackToOrderedMapRejectsTruncatedInputs(t *testing.T) {
	cases := [][]byte{
		{msgpackMap16, 0x00},
		{msgpackFixMapMin | 0x01, msgpackFixStrMin | 0x03, 'a'},
		{msgpackFixMapMin | 0x01, msgpackFixStrMin | 0x01, 'a', msgpackBin16, 0x00},
		{msgpackFixMapMin | 0x01, msgpackFixStrMin | 0x01, 'a', msgpackExt8, 0x02, byte(orderedMapExtCode), 0x81},
	}
	for _, data := range cases {
		if _, err := MsgpackToOrderedMap(data); err == nil {
			t.Fatalf("MsgpackToOrderedMap should fail for truncated input %x", data)
		}
	}
}

func TestOrderedMapExtRejectsMalformedPayload(t *testing.T) {
	payload, err := msgpack.Marshal(internalOrderedMap{
		Keys: []string{"a", "b"},
		Vals: []any{int64(1)},
	})
	if err != nil {
		t.Fatalf("marshal malformed payload: %v", err)
	}

	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "ext8", data: appendExtPayload(msgpackExt8, payload)},
		{name: "ext16", data: appendExtPayload(msgpackExt16, payload)},
		{name: "ext32", data: appendExtPayload(msgpackExt32, payload)},
		{name: "fixext", data: append([]byte{msgpackFixExt1, byte(orderedMapExtCode)}, payload[:1]...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := appendMapHeader(nil, 1)
			data = appendString(data, "ext")
			data = append(data, tc.data...)
			if _, err := MsgpackToOrderedMap(data); err == nil {
				t.Fatalf("MsgpackToOrderedMap should reject malformed ordered map ext")
			}
		})
	}
}

func valueOf(t *testing.T, om *orderedmap.OrderedMap, key string) any {
	t.Helper()
	val, ok := om.Get(key)
	if !ok {
		t.Fatalf("%s key missing", key)
	}
	return val
}

func equalStrings(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func appendExtPayload(code byte, payload []byte) []byte {
	data := make([]byte, 0, 6+len(payload))
	switch code {
	case msgpackExt8:
		data = append(data, msgpackExt8, byte(len(payload)))
	case msgpackExt16:
		data = append(data, msgpackExt16, 0, 0)
		binary.BigEndian.PutUint16(data[len(data)-2:], uint16(len(payload)))
	case msgpackExt32:
		data = append(data, msgpackExt32, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(data[len(data)-4:], uint32(len(payload)))
	}
	data = append(data, byte(orderedMapExtCode))
	return append(data, payload...)
}
