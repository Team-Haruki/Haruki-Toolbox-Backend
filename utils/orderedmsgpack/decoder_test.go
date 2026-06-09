package orderedmsgpack

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v3"
)

func TestDecodeValue_NonStringMapKeyStringified(t *testing.T) {
	// map with int key: {1: "x"}
	r := bytes.NewReader([]byte{
		msgpackFixMapMin | 0x01,
		0x01,
		msgpackFixStrMin | 0x01, 'x',
	})
	got, err := decodeValue(r)
	if err != nil {
		t.Fatalf("decodeValue returned error: %v", err)
	}
	m, ok := got.(*orderedmap.OrderedMap)
	if !ok {
		t.Fatalf("decodeValue type = %T, want *orderedmap.OrderedMap", got)
	}
	v, exists := m.Get("1")
	if !exists || v != "x" {
		t.Fatalf("decoded map value = %v (exists=%v), want x", v, exists)
	}
}

func TestDecodeValue_ExtPayloadRawBytes(t *testing.T) {
	// ext8 size=2 type=1 payload=0xaa,0xbb
	r := bytes.NewReader([]byte{
		msgpackExt8, 0x02, 0x01, 0xaa, 0xbb,
	})
	got, err := decodeValue(r)
	if err != nil {
		t.Fatalf("decodeValue returned error: %v", err)
	}
	b, ok := got.([]byte)
	if !ok {
		t.Fatalf("decodeValue type = %T, want []byte", got)
	}
	if len(b) != 2 || b[0] != 0xaa || b[1] != 0xbb {
		t.Fatalf("decoded ext bytes = %x, want aabb", b)
	}
}

func TestDecodeValue_UnsupportedType(t *testing.T) {
	// 0xc1 is reserved / unsupported
	r := bytes.NewReader([]byte{0xc1})
	if _, err := decodeValue(r); err == nil {
		t.Fatalf("decodeValue should fail for unsupported type byte")
	}
}

func TestDecodeValue_Float32(t *testing.T) {
	r := bytes.NewReader([]byte{msgpackFloat32, 0x3f, 0x80, 0x00, 0x00}) // 1.0
	got, err := decodeValue(r)
	if err != nil {
		t.Fatalf("decodeValue returned error: %v", err)
	}
	f, ok := got.(float64)
	if !ok {
		t.Fatalf("decodeValue type = %T, want float64", got)
	}
	if math.Abs(f-1.0) > 1e-9 {
		t.Fatalf("decoded float32 = %v, want 1.0", f)
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
