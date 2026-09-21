package data

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"io"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/msgpackcodec"
)

func TestProviderJSONSimpleMap(t *testing.T) {
	t.Parallel()

	// {"a":1,"b":"x"}
	msgpack := []byte{
		0x82,
		0xa1, 'a',
		0x01,
		0xa1, 'b',
		0xa1, 'x',
	}
	var out bytes.Buffer
	if err := writeProviderJSON(msgpack, &out); err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if out.String() != `{"a":1,"b":"x"}` {
		t.Fatalf("Convert output = %q, want %q", out.String(), `{"a":1,"b":"x"}`)
	}
}

func TestProviderJSONSimpleArray(t *testing.T) {
	t.Parallel()

	// [1,true,null,"x"]
	msgpack := []byte{
		0x94,
		0x01,
		0xc3,
		0xc0,
		0xa1, 'x',
	}
	var out bytes.Buffer
	if err := writeProviderJSON(msgpack, &out); err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if out.String() != `[1,true,null,"x"]` {
		t.Fatalf("Convert output = %q, want %q", out.String(), `[1,true,null,"x"]`)
	}
}

func TestProviderJSONInvalidData(t *testing.T) {
	t.Parallel()

	// Truncated fixmap: key exists but value is missing.
	msgpack := []byte{0x81, 0xa1, 'a'}
	var out bytes.Buffer
	if err := writeProviderJSON(msgpack, &out); err == nil {
		t.Fatalf("Convert should fail for invalid truncated msgpack")
	}
}

func TestProviderJSONMapNonStringKeyFails(t *testing.T) {
	t.Parallel()

	// {1:1} is invalid for JSON object conversion because key is not msgpack string.
	msgpack := []byte{
		0x81,
		0x01,
		0x01,
	}
	var out bytes.Buffer
	if err := writeProviderJSON(msgpack, &out); err == nil {
		t.Fatalf("Convert should fail when map key is not string")
	}
}

func TestProviderJSONBinaryAndExtAsNull(t *testing.T) {
	t.Parallel()

	// [bin8("ab"), ext8(type=1,payload=2), fixext4(type=1,payload=4 bytes)]
	msgpack := []byte{
		0x93,
		0xc4, 0x02, 'a', 'b',
		0xc7, 0x02, 0x01, 0x10, 0x20,
		0xd6, 0x01, 0xaa, 0xbb, 0xcc, 0xdd,
	}
	var out bytes.Buffer
	if err := writeProviderJSON(msgpack, &out); err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if out.String() != `[null,null,null]` {
		t.Fatalf("Convert output = %q, want %q", out.String(), `[null,null,null]`)
	}
}

func TestProviderJSONAddsUserIDStringForUserGamedata(t *testing.T) {
	t.Parallel()

	// {"userGamedata":{"userId":9223372036854775000,"rank":123}}
	msgpack := []byte{
		0x81,
		0xac, 'u', 's', 'e', 'r', 'G', 'a', 'm', 'e', 'd', 'a', 't', 'a',
		0x82,
		0xa6, 'u', 's', 'e', 'r', 'I', 'd',
		0xd3, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfc, 0xd8,
		0xa4, 'r', 'a', 'n', 'k',
		0x7b,
	}
	var out bytes.Buffer
	if err := writeProviderJSON(msgpack, &out); err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	want := `{"userGamedata":{"userId":9223372036854775000,"rank":123,"userIdString":"9223372036854775000"}}`
	if out.String() != want {
		t.Fatalf("Convert output = %q, want %q", out.String(), want)
	}
}

func TestProviderJSONRejectsDuplicateNamesAndInvalidUTF8(t *testing.T) {
	for _, data := range [][]byte{
		{0x82, 0xa1, 'a', 1, 0xa1, 'a', 2},
		{0xa1, 0xff},
		{1, 2},
	} {
		var out bytes.Buffer
		if err := writeProviderJSON(data, &out); err == nil {
			t.Fatal("invalid JSON source accepted")
		}
	}
}

func TestProviderJSONDerivesIDStringWithoutDuplicateOutput(t *testing.T) {
	input, err := msgpackcodec.Marshal(map[string]any{"userGamedata": map[string]any{"userId": int64(9007199254740993), "userIdString": "untrusted"}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeProviderJSON(input, &out); err != nil {
		t.Fatal(err)
	}
	var result map[string]map[string]jsontext.Value
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if string(result["userGamedata"]["userIdString"]) != `"9007199254740993"` {
		t.Fatalf("incorrect derived ID: %s", out.Bytes())
	}
}

func writeProviderJSON(data []byte, w io.Writer) error {
	return msgpackcodec.WriteJSON(w, data, ProviderJSONOptions())
}
