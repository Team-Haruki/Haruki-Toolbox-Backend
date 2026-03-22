package orderedmsgpack

import (
	"bytes"
	"math"
	"testing"

	"github.com/iancoleman/orderedmap"
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
