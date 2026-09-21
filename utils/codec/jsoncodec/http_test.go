package jsoncodec

import (
	"bytes"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestJSONContract(t *testing.T) {
	type payload struct {
		Zero     int            `json:"zero,omitzero"`
		Optional *bool          `json:"optional,omitzero"`
		Slice    []int          `json:"slice"`
		Map      map[string]int `json:"map"`
		ID       uint64         `json:"id"`
	}
	v := payload{Optional: new(false), ID: 18446744073709551615}
	data, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte(`{"optional":false,"slice":null,"map":null,"id":18446744073709551615}`)) {
		t.Fatalf("unexpected wire contract: %s", data)
	}
	for _, raw := range []string{`{"id":1,"id":2}`, "{\"id\":\"\xff\"}"} {
		if err := Unmarshal([]byte(raw), &v); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestRestyUsesJSONv2(t *testing.T) {
	client := ConfigureResty(resty.New())
	var value map[string]any
	if err := client.JSONUnmarshal([]byte(`{"a":1,"a":2}`), &value); err == nil {
		t.Fatal("Resty accepted duplicate names")
	}
}
