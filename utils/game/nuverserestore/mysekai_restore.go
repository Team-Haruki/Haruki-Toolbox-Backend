package nuverserestore

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
)

const HarvestMaps = "userMysekaiHarvestMaps"

// Extra retains future positional fields without guessing their names.
const Extra = "_msgpackExtra"

var ErrRestoredJSONTooLarge = errors.New("restored harvest maps exceed the JSON size limit")

type definition struct {
	name     string
	kind     string
	nullable bool
	children []definition
}

// Document handles both flattened suite fields and MYSEKAI updatedResources.
// Input ownership is preserved, including when a later field is invalid.
func (r *MysekaiRestorer) Document(server string, data map[string]any) (map[string]any, error) {
	if r.Fingerprint(server) == "" {
		return data, nil
	}
	out := data
	if value, ok := data[HarvestMaps]; ok {
		restored, changed, err := restoreList(value, r.regions[server].fields, HarvestMaps)
		if err != nil {
			return nil, err
		}
		if changed {
			out = maps.Clone(out)
			out[HarvestMaps] = restored
		}
	}
	if updated, ok := data["updatedResources"].(map[string]any); ok {
		if value, present := updated[HarvestMaps]; present {
			restored, changed, err := restoreList(value, r.regions[server].fields, "updatedResources."+HarvestMaps)
			if err != nil {
				return nil, err
			}
			if changed {
				out = maps.Clone(out)
				copy := maps.Clone(updated)
				copy[HarvestMaps] = restored
				out["updatedResources"] = copy
			}
		}
	}
	return out, nil
}

// JSON restores one stored harvest-map column, preserving exact numbers and
// returning the original bytes when the column contains only old objects.
func (r *MysekaiRestorer) JSON(server string, raw []byte) ([]byte, error) {
	return r.JSONWithLimit(server, raw, math.MaxInt)
}

// JSONWithLimit caps the restored encoding before storing it in memory.
func (r *MysekaiRestorer) JSONWithLimit(server string, raw []byte, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, ErrRestoredJSONTooLarge
	}
	if r.Fingerprint(server) != "" {
		var value any
		if err := json.UnmarshalRead(bytes.NewReader(raw), &value, jsonvalue.Numbers); err != nil {
			return nil, err
		}
		restored, changed, err := restoreList(value, r.regions[server].fields, HarvestMaps)
		if err != nil {
			return nil, err
		}
		if changed {
			out := &boundedJSON{limit: limit}
			if err := json.MarshalWrite(out, restored); err != nil {
				return nil, err
			}
			return out.Bytes(), nil
		}
	}
	if len(raw) > limit {
		return nil, ErrRestoredJSONTooLarge
	}
	return raw, nil
}

type boundedJSON struct {
	bytes.Buffer
	limit int
}

func (b *boundedJSON) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, ErrRestoredJSONTooLarge
	}
	return b.Buffer.Write(p)
}

func restoreList(value any, defs []definition, path string) (any, bool, error) {
	if value == nil {
		return value, false, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false, fmt.Errorf("%s: expected array", path)
	}
	var out []any
	for i, item := range items {
		restored, changed, err := restoreRecord(item, defs, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, false, err
		}
		if changed {
			if out == nil {
				out = append([]any(nil), items...)
			}
			out[i] = restored
		}
	}
	if out == nil {
		return value, false, nil
	}
	return out, true, nil
}

func restoreRecord(value any, defs []definition, path string) (any, bool, error) {
	switch v := value.(type) {
	case map[string]any:
		out := v
		changed := false
		for _, d := range defs {
			child, ok := v[d.name]
			if !ok || d.kind != "array" {
				continue
			}
			restored, c, err := restoreList(child, d.children, path+"."+d.name)
			if err != nil {
				return nil, false, err
			}
			if c {
				if !changed {
					out = maps.Clone(v)
					changed = true
				}
				out[d.name] = restored
			}
		}
		return out, changed, nil
	case []any:
		out := make(map[string]any, len(defs))
		for i, d := range defs {
			if i >= len(v) {
				if d.nullable {
					continue
				}
				return nil, false, fmt.Errorf("%s: missing %s at index %d", path, d.name, i)
			}
			child := v[i]
			if child == nil && d.nullable {
				out[d.name] = nil
				continue
			}
			if d.kind == "array" {
				if child == nil {
					out[d.name] = nil
					continue
				}
				restored, _, err := restoreList(child, d.children, path+"."+d.name)
				if err != nil {
					return nil, false, err
				}
				out[d.name] = restored
			} else {
				valid := false
				switch d.kind {
				case "int", "long":
					valid = integer(child)
				case "string":
					_, valid = child.(string)
				case "boolean":
					_, valid = child.(bool)
				case "float", "double":
					valid = numeric(child)
				}
				if !valid {
					return nil, false, fmt.Errorf("%s.%s: expected %s", path, d.name, d.kind)
				}
				out[d.name] = child
			}
		}
		if len(v) > len(defs) {
			out[Extra] = append([]any(nil), v[len(defs):]...)
		}
		return out, true, nil
	default:
		return nil, false, fmt.Errorf("%s: expected record or positional array", path)
	}
}

func integer(v any) bool {
	if n, ok := v.(jsonvalue.Number); ok {
		_, err := n.Int64()
		return err == nil
	}
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		return !math.IsInf(f, 0) && !math.IsNaN(f) && math.Trunc(f) == f
	}
	return false
}

func numeric(v any) bool {
	if n, ok := v.(jsonvalue.Number); ok {
		f, err := n.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	if integer(v) {
		return true
	}
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Float32 || rv.Kind() == reflect.Float64 {
		return !math.IsNaN(rv.Float()) && !math.IsInf(rv.Float(), 0)
	}
	return false
}
