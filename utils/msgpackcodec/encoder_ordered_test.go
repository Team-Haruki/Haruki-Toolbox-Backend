package msgpackcodec

import (
	"bytes"
	json "encoding/json/v2"
	"reflect"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
)

func TestOrderedRoundTrip(t *testing.T) {
	nested := orderedmap.New()
	nested.Set("last", int64(9007199254740993))
	nested.Set("first", []byte{0, 255})
	root := orderedmap.New()
	root.Set("z", []any{nested, nil, true})
	root.Set("a", uint64(18446744073709551615))
	data, err := Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeOrdered(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root.Keys(), got.Keys()) {
		t.Fatal("root order changed")
	}
	values, ok := got.GetAs[[]any]("z")
	if !ok {
		t.Fatal("array missing")
	}
	object := values[0].(*orderedmap.OrderedMap)
	if !reflect.DeepEqual(object.Keys(), nested.Keys()) {
		t.Fatal("nested order changed")
	}
	if n, ok := object.GetAs[uint64]("last"); !ok || n != 9007199254740993 {
		t.Fatal("integer lost precision")
	}
	if n, ok := got.GetAs[uint64]("a"); !ok || n != 18446744073709551615 {
		t.Fatal("uint64 lost precision")
	}
	var writer bytes.Buffer
	if err := MarshalWrite(&writer, root); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, writer.Bytes()) {
		t.Fatal("writer differs")
	}
}

func TestOrderedMarshalRejectsCycle(t *testing.T) {
	root := orderedmap.New()
	root.Set("self", root)
	if _, err := Marshal(root); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestOrderedMapHeaderAllocationBound(t *testing.T) {
	if _, err := DecodeOrdered([]byte{0xdf, 0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Fatal("huge truncated map accepted")
	}
}

func TestOrderedUnmarshalEntryPoints(t *testing.T) {
	input := []byte{0x82, 0xa1, 'z', 1, 0xa1, 'a', 2}
	var value orderedmap.OrderedMap
	value.Set("old", true)
	if err := Unmarshal(input, &value); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value.Keys(), []string{"z", "a"}) {
		t.Fatal("unmarshal did not replace the object in wire order")
	}
	var pointer *orderedmap.OrderedMap
	if err := UnmarshalRead(bytes.NewReader(input), &pointer); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pointer.Keys(), value.Keys()) {
		t.Fatal("reader changed order")
	}
	if err := Unmarshal([]byte{0x81}, &value); err == nil {
		t.Fatal("malformed object accepted")
	}
	if !reflect.DeepEqual(value.Keys(), pointer.Keys()) {
		t.Fatal("failed decode changed destination")
	}
	if err := Unmarshal(input, (*orderedmap.OrderedMap)(nil)); err == nil {
		t.Fatal("nil target accepted")
	}
}

func TestOrderedDestinationRemainsMutableAfterStorageGrowth(t *testing.T) {
	// Unmarshal assigns a decoded small map into the caller's existing value.
	// Its entries may initially belong to the decoder's combined allocation.
	input := []byte{0x82, 0xa1, 'z', 1, 0xa1, 'a', 2}
	var value orderedmap.OrderedMap
	if err := Unmarshal(input, &value); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"b", "c", "d", "e", "f", "g", "h"} {
		value.Set(k, true)
	}
	value.Set("z", "updated")
	value.Delete("a")
	if !reflect.DeepEqual(value.Keys(), []string{"z", "b", "c", "d", "e", "f", "g", "h"}) {
		t.Fatal("growth changed wire order")
	}
	if v, ok := value.GetAs[string]("z"); !ok || v != "updated" {
		t.Fatal("growth lost replacement")
	}
	encoded, err := Marshal(&value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOrdered(encoded)
	if err != nil || !reflect.DeepEqual(decoded.Keys(), value.Keys()) {
		t.Fatalf("grown map round trip: %v", err)
	}
}

func FuzzOrderedRoundTrip(f *testing.F) {
	f.Add([]byte{0x80})
	f.Add([]byte{0x82, 0xa1, 'z', 1, 0xa1, 'a', 0x91, 2})
	f.Add([]byte{0x82, 0xa1, 'a', 1, 0xa1, 'a', 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8192 {
			return
		}
		value, err := DecodeOrdered(data)
		if err != nil {
			return
		}
		encoded, err := Marshal(value)
		if err != nil {
			t.Fatalf("decoded object cannot be encoded: %v", err)
		}
		decoded, err := DecodeOrdered(encoded)
		if err != nil {
			t.Fatalf("round trip cannot be decoded: %v", err)
		}
		if !reflect.DeepEqual(value.Keys(), decoded.Keys()) {
			t.Fatal("round trip changed key order")
		}
		before, errBefore := json.Marshal(value)
		after, errAfter := json.Marshal(decoded)
		if (errBefore == nil) != (errAfter == nil) {
			t.Fatal("round trip changed JSON validity")
		}
		if errBefore == nil && !bytes.Equal(before, after) {
			t.Fatalf("round trip changed values: %s -> %s", before, after)
		}
	})
}
