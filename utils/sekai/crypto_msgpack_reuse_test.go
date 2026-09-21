package sekai

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/msgpackcodec"
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
