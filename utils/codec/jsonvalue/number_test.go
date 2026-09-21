package jsonvalue

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

func TestNumbersNestedRoundTrip(t *testing.T) {
	payload := []byte(`{"id":9007199254740993,"nested":[18446744073709551615,1.2500e+3,{"s":"日本語","b":false,"n":null}]}`)
	var value any
	if err := json.Unmarshal(payload, &value, Numbers); err != nil {
		t.Fatal(err)
	}
	object := value.(map[string]any)
	if object["id"] != Number("9007199254740993") {
		t.Fatalf("id lost precision: %v", object["id"])
	}
	nested := object["nested"].([]any)
	if nested[0] != Number("18446744073709551615") || nested[1] != Number("1.2500e+3") {
		t.Fatal("numeric text changed")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "9007199254740993") || !strings.Contains(string(encoded), "1.2500e+3") {
		t.Fatalf("round trip lost numeric text: %s", encoded)
	}
}

func TestNumbersRejectMalformedInput(t *testing.T) {
	for _, data := range []string{`{"id":1,"id":2}`, `1 2`, `[01]`, "\"\xff\"", strings.Repeat("[", 258) + "0" + strings.Repeat("]", 258)} {
		var value any
		if err := json.Unmarshal([]byte(data), &value, Numbers); err == nil {
			t.Fatalf("invalid input accepted (length %d)", len(data))
		}
	}
	for _, value := range []Number{"", "NaN", "01", "1,2", `"1"`} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid number accepted: %q", value)
		}
	}
}

func TestNumbersInTypedStruct(t *testing.T) {
	var value struct {
		Count int `json:"count"`
		Data  any `json:"data"`
	}
	if err := json.Unmarshal([]byte(`{"count":2,"data":9007199254740993}`), &value, Numbers); err != nil {
		t.Fatal(err)
	}
	if value.Count != 2 || value.Data != Number("9007199254740993") {
		t.Fatal("typed and dynamic values decoded incorrectly")
	}
}
