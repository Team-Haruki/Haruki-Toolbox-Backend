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

func TestNumberConversions(t *testing.T) {
	if got, err := Number("9007199254740993").Int64(); err != nil || got != 9007199254740993 {
		t.Fatalf("Int64 = %d, %v", got, err)
	}
	if got, err := Number("-1.25e2").Float64(); err != nil || got != -125 {
		t.Fatalf("Float64 = %v, %v", got, err)
	}
	for _, n := range []Number{"1.5", "18446744073709551615", "abc"} {
		if _, err := n.Int64(); err == nil {
			t.Fatalf("Int64 accepted %q", n)
		}
	}
	if _, err := Number("1e400").Float64(); err == nil {
		t.Fatal("Float64 accepted an out-of-range value")
	}
}

func TestNumberUnmarshalPreservesText(t *testing.T) {
	var value struct {
		ID    Number  `json:"id"`
		Ratio *Number `json:"ratio"`
	}
	if err := json.Unmarshal([]byte(`{"id":18446744073709551616,"ratio":1.50e+0}`), &value); err != nil {
		t.Fatal(err)
	}
	if value.ID != "18446744073709551616" || value.Ratio == nil || *value.Ratio != "1.50e+0" {
		t.Fatalf("numeric text changed: %q %v", value.ID, value.Ratio)
	}
	for _, data := range []string{`{"id":"1"}`, `{"id":true}`, `{"id":[1]}`, `{"id":01}`} {
		var bad struct {
			ID Number `json:"id"`
		}
		if err := json.Unmarshal([]byte(data), &bad); err == nil {
			t.Fatalf("non-number accepted: %s", data)
		}
	}
}

func TestNumbersDecodesLiteralsAndRejectsNestedErrors(t *testing.T) {
	var value any
	if err := json.Unmarshal([]byte(`{"t":true,"f":false,"n":null,"a":[]}`), &value, Numbers); err != nil {
		t.Fatal(err)
	}
	object := value.(map[string]any)
	if object["t"] != true || object["f"] != false || object["n"] != nil {
		t.Fatalf("literals decoded incorrectly: %#v", object)
	}
	if a, ok := object["a"].([]any); !ok || len(a) != 0 {
		t.Fatalf("empty array decoded as %#v", object["a"])
	}
	for _, data := range []string{`{"a":01}`, `{"a":{"b":[1,]}}`, `[{"a":1}`, `{"a":1`} {
		var bad any
		if err := json.Unmarshal([]byte(data), &bad, Numbers); err == nil {
			t.Fatalf("malformed input accepted: %s", data)
		}
	}
}
