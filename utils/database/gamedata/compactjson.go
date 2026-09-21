package gamedata

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"strconv"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/jsonvalue"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/compactrestore"
)

// IsCompactValue reports whether a stored value is in the columnar form.
//
// The stored value is self-describing and needs no metadata column: a JSON
// object is compact, a JSON array is untouched row form. Both live in the same
// column — measured on production, user_costume3d_statuses_j holds 6,570 compact
// objects (cn/tw/kr) and 4,148 row arrays (jp/en) at the same time.
func IsCompactValue(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

// ExpandCompactJSON turns a stored columnar value into the row-form JSON array a
// client expects, preserving the production restore semantics.
// Column names are encoded once and rows are written directly to the output.
//
// A value that is already row form is returned untouched, so callers can hand
// every compact-class column through this function without inspecting it first.
func ExpandCompactJSON(raw []byte) ([]byte, error) {
	if !IsCompactValue(raw) {
		return raw, nil
	}
	// Only this call borrows scalar bytes. The input remains live and immutable
	// until the separately allocated output is complete; no parse tree escapes.
	parsed, err := parseOrderedInput(raw, raw)
	if err != nil {
		return nil, fmt.Errorf("gamedata: parse compact value: %w", err)
	}
	doc, ok := parsed.(orderedDoc)
	if !ok {
		return nil, fmt.Errorf("gamedata: compact value is not an object")
	}
	var enums orderedDoc
	if value, ok := doc.get(compactrestore.EnumKey); ok {
		enums, _ = value.(orderedDoc)
	}
	type column struct {
		key        []byte
		values     []any
		dict       []any
		enumerated bool
	}
	columns := make([]column, 0, len(doc))
	rows := -1
	for _, p := range doc {
		if p.Key == compactrestore.EnumKey {
			continue
		}
		key, err := json.Marshal(p.Key)
		if err != nil {
			return nil, err
		}
		// Non-array columns have length zero; rows stop at the shortest column.
		values, _ := p.Val.([]any)
		enumValue, enumerated := enums.get(p.Key)
		dict, _ := enumValue.([]any)
		columns = append(columns, column{key, values, dict, enumerated})
		if rows < 0 || len(values) < rows {
			rows = len(values)
		}
	}
	out := make([]byte, 0, len(raw)*2)
	out = append(out, '[')
	for row := 0; row < rows; row++ {
		if row > 0 {
			out = append(out, ',')
		}
		out = append(out, '{')
		for i, c := range columns {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, c.key...)
			out = append(out, ':')
			v := c.values[row]
			if c.enumerated {
				// Preserve the former rawIndicesToInt + RestoreColumns contract:
				// only integer literals index a dictionary. Float/exponent/string
				// indices, overflow and out-of-range values become null.
				var mapped any
				if text, ok := v.(jsontext.Value); ok {
					if index, err := strconv.Atoi(string(text)); err == nil && index >= 0 && index < len(c.dict) {
						mapped = c.dict[index]
					}
				}
				v = mapped
			}
			out, err = appendJSON(out, v)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, '}')
	}
	return append(out, ']'), nil
}

// --- order-preserving JSON ---------------------------------------------------
//
// encoding/json's map[string]any loses object key order and rewrites every
// number through float64, which corrupts game user ids above 2^53
// (28808221489823746 is real). Scalars are therefore kept as their exact source
// bytes and objects keep their order.

type orderedPair struct {
	Key string
	Val any
}

type orderedDoc []orderedPair

func (d orderedDoc) get(key string) (any, bool) {
	for _, p := range d {
		if p.Key == key {
			return p.Val, true
		}
	}
	return nil, false
}

func parseOrdered(b []byte) (any, error) {
	return parseOrderedInput(b, nil)
}

// input is nil for the ordinary owning parser, or b for the compact-only
// borrowing path. Scalar offsets refer to the original input, never to the
// decoder's reusable buffer.
func parseOrderedInput(b, input []byte) (any, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(b))
	v, err := parseOrderedValue(dec, input)
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		return nil, fmt.Errorf("trailing bytes after JSON value")
	}
	return v, nil
}

func parseOrderedValue(dec *jsontext.Decoder, input []byte) (any, error) {
	if dec.StackDepth() > 256 {
		return nil, fmt.Errorf("JSON nesting exceeds 256")
	}
	switch dec.PeekKind() {
	case '{':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		doc := orderedDoc{}
		for dec.PeekKind() != '}' {
			key, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			name := key.String()
			value, err := parseOrderedValue(dec, input)
			if err != nil {
				return nil, err
			}
			doc = append(doc, orderedPair{Key: name, Val: value})
		}
		_, err := dec.ReadToken()
		return doc, err
	case '[':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		arr := []any{}
		for dec.PeekKind() != ']' {
			value, err := parseOrderedValue(dec, input)
			if err != nil {
				return nil, err
			}
			arr = append(arr, value)
		}
		_, err := dec.ReadToken()
		return arr, err
	default:
		value, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		if value.Kind() == 'n' {
			return nil, nil
		}
		if input != nil {
			end := int(dec.InputOffset())
			return jsontext.Value(input[end-len(value) : end : end]), nil
		}
		return value.Clone(), nil
	}
}

// appendJSON renders a parsed value back to JSON text, preserving the exact
// scalar bytes it was parsed from.
func appendJSON(dst []byte, v any) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return append(dst, "null"...), nil
	case jsontext.Value:
		return append(dst, t...), nil
	case jsonvalue.Number:
		return append(dst, t.String()...), nil
	case string:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, err
		}
		return append(dst, b...), nil
	case orderedDoc:
		dst = append(dst, '{')
		for i, p := range t {
			if i > 0 {
				dst = append(dst, ',')
			}
			kb, err := json.Marshal(p.Key)
			if err != nil {
				return nil, err
			}
			dst = append(dst, kb...)
			dst = append(dst, ':')
			if dst, err = appendJSON(dst, p.Val); err != nil {
				return nil, err
			}
		}
		return append(dst, '}'), nil
	case []any:
		dst = append(dst, '[')
		for i, e := range t {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = appendJSON(dst, e); err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return nil, fmt.Errorf("gamedata: cannot render %T as JSON: %w", v, err)
		}
		return append(dst, b...), nil
	}
}
