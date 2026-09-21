package jsonvalue

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
)

// ScalarString derives text from a valid JSON number or nonempty string.
// Numeric spelling is preserved, including fractions and exponents. Null,
// booleans, containers and malformed JSON do not produce a derived value.
func ScalarString(raw []byte) (string, bool) {
	value := jsontext.Value(raw)
	switch value.Kind() {
	case '"':
		var text string
		if err := json.Unmarshal(value, &text); err != nil || text == "" {
			return "", false
		}
		return text, true
	case '0':
		if !value.IsValid() {
			return "", false
		}
		return string(raw), true
	default:
		return "", false
	}
}
