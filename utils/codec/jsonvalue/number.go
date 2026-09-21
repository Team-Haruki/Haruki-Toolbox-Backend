// Package jsonvalue preserves exact JSON numbers at dynamically typed game
// data boundaries. All syntax validation and encoding use JSON v2/jsontext.
package jsonvalue

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"strconv"
)

type Number string

func (n Number) String() string            { return string(n) }
func (n Number) Int64() (int64, error)     { return strconv.ParseInt(string(n), 10, 64) }
func (n Number) Float64() (float64, error) { return strconv.ParseFloat(string(n), 64) }
func (n Number) MarshalJSONTo(enc *jsontext.Encoder) error {
	value := jsontext.Value(n)
	if value.Kind() != '0' || !value.IsValid() {
		return fmt.Errorf("invalid JSON number %q", n)
	}
	return enc.WriteValue(value)
}
func (n *Number) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	value, err := dec.ReadValue()
	if err != nil {
		return err
	}
	if value.Kind() != '0' {
		return fmt.Errorf("expected JSON number")
	}
	*n = Number(value)
	return nil
}

// Numbers decodes interface values without converting numeric tokens to
// float64. Concrete struct numeric fields continue using their declared types.
var Numbers = json.WithUnmarshalers(json.UnmarshalFromFunc(decodeAny))

func decodeAny(dec *jsontext.Decoder, out *any) error {
	if dec.StackDepth() > 256 {
		return fmt.Errorf("JSON nesting exceeds 256")
	}
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	switch tok.Kind() {
	case '{':
		object := make(map[string]any)
		for dec.PeekKind() != '}' {
			key, err := dec.ReadToken()
			if err != nil {
				return err
			}
			name := key.String()
			var value any
			if err := decodeAny(dec, &value); err != nil {
				return err
			}
			object[name] = value
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		*out = object
	case '[':
		array := []any{}
		for dec.PeekKind() != ']' {
			var value any
			if err := decodeAny(dec, &value); err != nil {
				return err
			}
			array = append(array, value)
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		*out = array
	case '0':
		*out = Number(tok.String())
	case '"':
		*out = tok.String()
	case 't':
		*out = true
	case 'f':
		*out = false
	case 'n':
		*out = nil
	default:
		return fmt.Errorf("unexpected JSON token %v", tok.Kind())
	}
	return nil
}
