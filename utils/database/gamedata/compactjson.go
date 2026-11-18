package gamedata

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"strconv"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/nuverserestore"
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
	out := compactJSONOutput{
		body:  make([]byte, 0, min(len(raw), 1<<20)),
		limit: DefaultLimits().MaxKeyBytes,
	}
	if err := renderCompactJSON(raw, &out); err != nil {
		return nil, err
	}
	return out.body, nil
}

func renderCompactJSON(raw []byte, out *compactJSONOutput) error {
	// Only this call borrows scalar bytes. The input remains live and immutable
	// until the separately allocated output is complete; no parse tree escapes.
	parsed, err := parseOrderedInput(raw, raw)
	if err != nil {
		return fmt.Errorf("gamedata: parse compact value: %w", err)
	}
	doc, ok := parsed.(orderedDoc)
	if !ok {
		return fmt.Errorf("gamedata: compact value is not an object")
	}
	var enums orderedDoc
	enumKey := nuverserestore.EnumKey
	if value, ok := doc.get(nuverserestore.EnumKey); ok {
		enums, _ = value.(orderedDoc)
	} else if value, ok := doc.get(nuverserestore.EnumKeyAlias); ok {
		if aliased, isObject := value.(orderedDoc); isObject {
			enums, enumKey = aliased, nuverserestore.EnumKeyAlias
		}
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
		if p.Key == nuverserestore.EnumKey || p.Key == enumKey {
			continue
		}
		key, err := json.Marshal(p.Key)
		if err != nil {
			return err
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
	out.append("[")
	for row := 0; row < rows && out.err == nil; row++ {
		if row > 0 {
			out.append(",")
		}
		out.append("{")
		for i, c := range columns {
			if i > 0 {
				out.append(",")
			}
			out.appendBytes(c.key)
			out.append(":")
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
			out.value(v)
			if out.err != nil {
				return out.err
			}
		}
		out.append("}")
	}
	out.append("]")
	return out.err
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

// compactJSONOutput can count encoded bytes without building the expanded body.
type compactJSONOutput struct {
	body  []byte
	size  int
	limit int
	err   error
}

func (out *compactJSONOutput) append(raw string) {
	if out.reserve(len(raw)) && out.body != nil {
		out.body = append(out.body, raw...)
	}
}

func (out *compactJSONOutput) appendBytes(raw []byte) {
	if out.reserve(len(raw)) && out.body != nil {
		out.body = append(out.body, raw...)
	}
}

func (out *compactJSONOutput) reserve(n int) bool {
	if out.err != nil {
		return false
	}
	if n > out.limit-out.size {
		out.err = fmt.Errorf("gamedata: expanded compact value exceeds %d bytes", out.limit)
		return false
	}
	out.size += n
	return true
}

// value renders a parsed value while preserving the exact scalar bytes.
func (out *compactJSONOutput) value(value any) {
	if out.err != nil {
		return
	}
	switch v := value.(type) {
	case nil:
		out.append("null")
	case jsontext.Value:
		out.appendBytes(v)
	case jsonvalue.Number:
		out.append(v.String())
	case orderedDoc:
		out.append("{")
		for i, pair := range v {
			if i > 0 {
				out.append(",")
			}
			out.value(pair.Key)
			out.append(":")
			out.value(pair.Val)
			if out.err != nil {
				return
			}
		}
		out.append("}")
	case []any:
		out.append("[")
		for i, element := range v {
			if i > 0 {
				out.append(",")
			}
			out.value(element)
			if out.err != nil {
				return
			}
		}
		out.append("]")
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			out.err = fmt.Errorf("gamedata: cannot render %T as JSON: %w", v, err)
			return
		}
		out.appendBytes(encoded)
	}
}
