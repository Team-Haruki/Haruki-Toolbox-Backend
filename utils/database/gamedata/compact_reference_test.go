// Frozen pre-optimization implementation used for byte/error differential tests.
// Keep this test-only oracle independent of the optimized parser and row writer.
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

// referenceProductionRestoreOptions are the exact options the serving path uses today
// (utils/api/data/utils.go RestoreCompactData). They are not tunable here: the
// whole point of expanding in this package is to produce the same bytes the
// MongoDB path produced, and both of these change the output.
//
//   - NullInvalidEnumValue: an index outside the dictionary becomes null rather
//     than being passed through.
//   - ParseFloatEnumIndex: float64 is accepted as an index. Without it a
//     JSON-decoded index would silently fail to resolve and every enum column
//     would come back null.
var referenceProductionRestoreOptions = compactrestore.Options{
	InvalidEnumValue:    compactrestore.NullInvalidEnumValue,
	ParseFloatEnumIndex: true,
}

// referenceIsCompactValue reports whether a stored value is in the columnar form.
//
// The stored value is self-describing and needs no metadata column: a JSON
// object is compact, a JSON array is untouched row form. Both live in the same
// column — measured on production, user_costume3d_statuses_j holds 6,570 compact
// objects (cn/tw/kr) and 4,148 row arrays (jp/en) at the same time.
func referenceIsCompactValue(raw []byte) bool {
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

// referenceExpandCompactJSON turns a stored columnar value into the row-form JSON array a
// client expects, using the PRODUCTION restore algorithm.
//
// A value that is already row form is returned untouched, so callers can hand
// every compact-class column through this function without inspecting it first.
func referenceExpandCompactJSON(raw []byte) ([]byte, error) {
	if !referenceIsCompactValue(raw) {
		return raw, nil
	}
	v, err := referenceParseOrdered(raw)
	if err != nil {
		return nil, fmt.Errorf("gamedata: parse compact value: %w", err)
	}
	doc, ok := v.(referenceOrderedDoc)
	if !ok {
		return nil, fmt.Errorf("gamedata: compact value is not an object")
	}

	// Mirrors extractColumnsAndLabels in utils/api/data/utils.go.
	var enumRaw referenceOrderedDoc
	if ev, ok := doc.get(compactrestore.EnumKey); ok {
		if ed, ok := ev.(referenceOrderedDoc); ok {
			enumRaw = ed
		}
	}
	columns := make([]compactrestore.Column, 0, len(doc))
	enumColumns := make(map[string][]any, len(doc))
	for _, p := range doc {
		if p.Key == compactrestore.EnumKey {
			continue
		}
		values, _ := p.Val.([]any)
		if values == nil {
			// A non-array column becomes an empty column, exactly as the
			// MongoDB path does via convertToInterfaceSlice.
			values = []any{}
		}
		if enumRaw != nil {
			if ec, ok := enumRaw.get(p.Key); ok {
				if ea, ok := ec.([]any); ok {
					enumColumns[p.Key] = ea
				} else {
					enumColumns[p.Key] = []any{}
				}
			}
		}
		if _, isEnum := enumColumns[p.Key]; isEnum {
			// compactrestore.enumIndex accepts Go int kinds and (with
			// ParseFloatEnumIndex) float64, but NEVER jsonvalue.Number. Scalars are
			// otherwise kept as exact JSON text so no number is reformatted, so
			// enum indices have to be converted here or every enum column would
			// restore to null.
			values = referenceRawIndicesToInt(values)
		}
		columns = append(columns, compactrestore.Column{Key: p.Key, Values: values})
	}

	rows := compactrestore.RestoreColumns(columns, enumColumns, referenceProductionRestoreOptions)

	out := make([]byte, 0, len(raw)*2)
	out = append(out, '[')
	for i, row := range rows {
		if i > 0 {
			out = append(out, ',')
		}
		d := make(referenceOrderedDoc, 0, len(row))
		for _, f := range row {
			d = append(d, referenceOrderedPair{Key: f.Key, Val: f.Value})
		}
		out, err = referenceAppendJSON(out, d)
		if err != nil {
			return nil, err
		}
	}
	return append(out, ']'), nil
}

func referenceRawIndicesToInt(values []any) []any {
	out := make([]any, len(values))
	for i, v := range values {
		rm, ok := v.(jsontext.Value)
		if !ok {
			out[i] = v
			continue
		}
		if string(rm) == "null" {
			out[i] = nil
			continue
		}
		n, err := strconv.Atoi(string(rm))
		if err != nil {
			out[i] = v
			continue
		}
		out[i] = n
	}
	return out
}

// --- order-preserving JSON ---------------------------------------------------
//
// encoding/json's map[string]any loses object key order and rewrites every
// number through float64, which corrupts game user ids above 2^53
// (28808221489823746 is real). Scalars are therefore kept as their exact source
// bytes and objects keep their order.

type referenceOrderedPair struct {
	Key string
	Val any
}

type referenceOrderedDoc []referenceOrderedPair

func (d referenceOrderedDoc) get(key string) (any, bool) {
	for _, p := range d {
		if p.Key == key {
			return p.Val, true
		}
	}
	return nil, false
}

func referenceParseOrdered(b []byte) (any, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(b))
	v, err := referenceParseOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		return nil, fmt.Errorf("trailing bytes after JSON value")
	}
	return v, nil
}

func referenceParseOrderedValue(dec *jsontext.Decoder) (any, error) {
	if dec.StackDepth() > 256 {
		return nil, fmt.Errorf("JSON nesting exceeds 256")
	}
	switch dec.PeekKind() {
	case '{':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		doc := referenceOrderedDoc{}
		for dec.PeekKind() != '}' {
			key, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			name := key.String()
			value, err := referenceParseOrderedValue(dec)
			if err != nil {
				return nil, err
			}
			doc = append(doc, referenceOrderedPair{Key: name, Val: value})
		}
		_, err := dec.ReadToken()
		return doc, err
	case '[':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		arr := []any{}
		for dec.PeekKind() != ']' {
			value, err := referenceParseOrderedValue(dec)
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
		return value.Clone(), nil
	}
}

// referenceAppendJSON renders a parsed value back to JSON text, preserving the exact
// scalar bytes it was parsed from.
func referenceAppendJSON(dst []byte, v any) ([]byte, error) {
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
	case referenceOrderedDoc:
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
			if dst, err = referenceAppendJSON(dst, p.Val); err != nil {
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
			if dst, err = referenceAppendJSON(dst, e); err != nil {
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
