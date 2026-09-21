package msgpackcodec_test

import (
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/msgpackcodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
)

func TestPublicEntryPoints(t *testing.T) {
	// The repeated z must overwrite its value without moving behind a.
	data := []byte{0x83, 0xa1, 'z', 1, 0xa1, 'a', 2, 0xa1, 'z', 3}
	if err := msgpackcodec.ValidateMaxDepth(data, msgpackcodec.DefaultMaxUploadDepth); err != nil {
		t.Fatal(err)
	}
	a, err := msgpackcodec.DecodeOrdered(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := msgpackcodec.DecodeOrderedRead(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var c orderedmap.OrderedMap
	if err := msgpackcodec.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	var d *orderedmap.OrderedMap
	if err := msgpackcodec.UnmarshalRead(bytes.NewReader(data), &d); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*orderedmap.OrderedMap{a, b, &c, d} {
		if !reflect.DeepEqual(m.Keys(), []string{"z", "a"}) {
			t.Fatal("wire order changed")
		}
		if z, ok := m.GetAs[int64]("z"); !ok || z != 3 {
			t.Fatal("duplicate key policy changed")
		}
		encoded, err := msgpackcodec.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := msgpackcodec.MarshalWrite(&out, m); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded, out.Bytes()) {
			t.Fatal("writer encoding changed")
		}
	}
	// Legacy generic destinations remain supported as well.
	var plain map[string]any
	if err := msgpackcodec.Unmarshal(data, &plain); err != nil || len(plain) != 2 {
		t.Fatalf("generic decode: %v", err)
	}
	if err := msgpackcodec.MarshalWrite(shortWriter{}, a); err != io.ErrShortWrite {
		t.Fatalf("short write: %v", err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }
