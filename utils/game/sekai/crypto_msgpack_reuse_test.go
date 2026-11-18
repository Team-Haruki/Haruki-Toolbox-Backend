package sekai

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/msgpackcodec"
)

func TestUnpackMsgpackMatchesEncryptedUnpack(t *testing.T) {
	c := testServerCryptor()
	for _, value := range []any{map[string]any{"id": int64(7486493092749597481), "nested": []any{nil, true, "name", map[string]any{"value": 123}}}, []any{1, "two", nil}, int64(123)} {
		raw, err := c.Pack(value, utils.SupportedDataUploadServerJP)
		if err != nil {
			t.Fatal(err)
		}
		want, err := c.Unpack(raw, utils.SupportedDataUploadServerJP)
		if err != nil {
			t.Fatal(err)
		}
		mp, err := c.DecryptToMsgpack(raw, utils.SupportedDataUploadServerJP)
		if err != nil {
			t.Fatal(err)
		}
		before := bytes.Clone(mp)
		got, err := UnpackMsgpack(mp)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) || !bytes.Equal(mp, before) {
			t.Fatalf("decode differs: got=%#v want=%#v", got, want)
		}
	}
}
func TestUnpackMsgpackRejectsDeepAndTruncatedInput(t *testing.T) {
	deep := append(bytes.Repeat([]byte{0x91}, msgpackcodec.DefaultMaxUploadDepth+1), 0xc0)
	for _, data := range [][]byte{deep, {0x81}, {0xdb, 0xff, 0xff, 0xff, 0xff}} {
		if _, err := UnpackMsgpack(data); err == nil {
			t.Fatal("unsafe msgpack accepted")
		}
	}
}

func TestUnpackMsgpackRejectsUnsupportedMapKey(t *testing.T) {
	data := []byte{0x81, 0xa1, 'k', 0x81, 0x90, 0xc0} // {"k": {[]: nil}}
	if _, err := UnpackMsgpack(data); err == nil || !strings.Contains(err.Error(), "msgpack validation: unsupported generic map key") {
		t.Fatalf("expected a map key validation error, got %v", err)
	}
}

func TestUnpackMsgpackKeepsBinaryRootKeyCompatibility(t *testing.T) {
	data := []byte{0x81, 0xc4, 0x01, 'k', 0xc3} // {bin("k"): true}
	got, err := UnpackMsgpack(data)
	if err != nil || !reflect.DeepEqual(got, map[string]any{"k": true}) {
		t.Fatalf("UnpackMsgpack = %#v, %v", got, err)
	}
}

func TestUnpackMsgpackOwnsBinaryValues(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "scalar", value: []byte("scalar")},
		{name: "array", value: []any{[]byte("array")}},
		{name: "map", value: map[string]any{
			"binary": []byte("map"),
			"nested": []any{map[string]any{"binary": []byte("nested")}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := msgpackcodec.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := UnpackMsgpack(data)
			if err != nil {
				t.Fatal(err)
			}
			clear(data)
			if !reflect.DeepEqual(got, tt.value) {
				t.Fatalf("decoded value changed after input reuse: got %#v, want %#v", got, tt.value)
			}
		})
	}
}
