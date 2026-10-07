package msgpackcodec

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
)

// Typed nil containers encode as MessagePack nil rather than an empty
// container, matching the generic encoder.
func TestOrderedMarshalNilContainers(t *testing.T) {
	for name, value := range map[string]any{
		"ordered_map": (*orderedmap.OrderedMap)(nil),
		"slice":       []any(nil),
		"map":         map[string]any(nil),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Marshal(value)
			if err != nil || !bytes.Equal(got, []byte{msgpackNil}) {
				t.Fatalf("Marshal(%T nil) = % x, %v; want c0", value, got, err)
			}
		})
	}
}

// An OrderedMap passed by value encodes exactly like a pointer to it.
func TestOrderedMarshalOrderedMapByValue(t *testing.T) {
	m := orderedmap.New()
	m.Set("b", int64(1))
	m.Set("a", "x")
	byPointer, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	byValue, err := Marshal(*m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(byPointer, byValue) {
		t.Fatalf("by value % x, by pointer % x", byValue, byPointer)
	}
	inSlice, err := Marshal([]any{*m})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inSlice[1:], byPointer) {
		t.Fatalf("OrderedMap value inside a slice encoded as % x", inSlice[1:])
	}
}

// Plain Go maps go through the ordered encoder too, so nested OrderedMaps
// inside them keep their order.
func TestOrderedMarshalPlainMapNestsOrderedValues(t *testing.T) {
	inner := orderedmap.New()
	inner.Set("z", int64(1))
	inner.Set("a", int64(2))
	data, err := Marshal(map[string]any{"inner": inner, "list": []any{"v"}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.GetAs[*orderedmap.OrderedMap]("inner")
	if !ok || !reflect.DeepEqual(got.Keys(), []string{"z", "a"}) {
		t.Fatalf("nested order lost: %v", got)
	}
	list, ok := decoded.GetAs[[]any]("list")
	if !ok || !reflect.DeepEqual(list, []any{"v"}) {
		t.Fatalf("list = %#v", list)
	}
}

// A self-referencing plain map or slice hits the depth limit instead of
// recursing forever.
func TestOrderedMarshalRejectsPlainCycles(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	if _, err := Marshal(m); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("map cycle: %v", err)
	}
	s := []any{nil}
	s[0] = s
	if _, err := Marshal(s); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("slice cycle: %v", err)
	}
	inner := orderedmap.New()
	inner.Set("cycle", m)
	if _, err := Marshal(inner); err == nil {
		t.Fatal("cycle below an ordered map accepted")
	}
	if err := MarshalWrite(&bytes.Buffer{}, m); err == nil {
		t.Fatal("MarshalWrite accepted a cycle")
	}
}

func TestAppendContainerHeaderWidths(t *testing.T) {
	tests := []struct {
		n      int
		object bool
		want   []byte
	}{
		{0, false, []byte{0x90}},
		{15, false, []byte{0x9f}},
		{16, false, []byte{0xdc, 0x00, 0x10}},
		{65535, false, []byte{0xdc, 0xff, 0xff}},
		{65536, false, []byte{0xdd, 0x00, 0x01, 0x00, 0x00}},
		{15, true, []byte{0x8f}},
		{16, true, []byte{0xde, 0x00, 0x10}},
		{65536, true, []byte{0xdf, 0x00, 0x01, 0x00, 0x00}},
	}
	for _, tc := range tests {
		got, err := appendContainerHeader(nil, tc.n, tc.object)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Fatalf("appendContainerHeader(%d, %v) = % x, %v; want % x", tc.n, tc.object, got, err, tc.want)
		}
	}
	if _, err := appendContainerHeader(nil, -1, false); err == nil {
		t.Fatal("negative container length accepted")
	}
}

// Containers above the fixed and 16-bit sizes round-trip through the ordered
// decoder with their order intact.
func TestOrderedMarshalLargeContainersRoundTrip(t *testing.T) {
	big := make([]any, 70000)
	for i := range big {
		big[i] = int64(i % 7)
	}
	m := orderedmap.New()
	keys := make([]string, 0, 20)
	for i := range 20 {
		k := string(rune('t' - i%20))
		k += strings.Repeat("k", i)
		m.Set(k, int64(i))
		keys = append(keys, k)
	}
	m.Set("big", big)
	data, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if data[0] != msgpackMap16 {
		t.Fatalf("21-entry map header = %#x, want map16", data[0])
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Keys(), append(keys, "big")) {
		t.Fatal("key order changed")
	}
	got, ok := decoded.GetAs[[]any]("big")
	if !ok || len(got) != len(big) || got[69999] != big[69999] {
		t.Fatalf("70000-element array did not round-trip (len %d)", len(got))
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestCodecEntryPointErrors(t *testing.T) {
	readErr := errors.New("read failed")

	if err := Unmarshal([]byte{0x81, 0xa1, 'a', 0x01}, (**orderedmap.OrderedMap)(nil)); err == nil ||
		!strings.Contains(err.Error(), "nil ordered map destination") {
		t.Fatalf("nil **OrderedMap: %v", err)
	}
	var dst *orderedmap.OrderedMap
	if err := Unmarshal([]byte{0x81}, &dst); err == nil || dst != nil {
		t.Fatalf("malformed input into **OrderedMap: err=%v dst=%v", err, dst)
	}
	if err := UnmarshalRead(failingReader{readErr}, &dst); !errors.Is(err, readErr) {
		t.Fatalf("UnmarshalRead ordered read error = %v", err)
	}
	if _, err := DecodeOrderedRead(failingReader{readErr}); !errors.Is(err, readErr) ||
		!strings.Contains(err.Error(), "read all") {
		t.Fatalf("DecodeOrderedRead read error = %v", err)
	}

	// Non-ordered destinations keep the legacy reader decoder.
	var plain map[string]any
	if err := UnmarshalRead(bytes.NewReader([]byte{0x81, 0xa1, 'a', 0x01}), &plain); err != nil || len(plain) != 1 {
		t.Fatalf("UnmarshalRead generic = %v, %v", plain, err)
	}
	var n int
	if err := UnmarshalRead(bytes.NewReader([]byte{0x05}), &n); err != nil || n != 5 {
		t.Fatalf("UnmarshalRead int = %d, %v", n, err)
	}
}
