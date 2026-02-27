// Package suiterestore converts suite user-data fields from compact array
// format to keyed dict (map) format.
//
// Field definitions are loaded from an external JSON file so that new
// field mappings can be added without recompilation. The JSON format
// supports recursive nesting for sub-array fields.
package suiterestore

import (
	"encoding/json"
	"fmt"
	"os"
)

// fieldDef describes one position in an array-to-dict mapping.
// It is either a simple key name, or a nested array field with its own keys.
type fieldDef struct {
	Key      string     // field name (always set)
	Children []fieldDef // non-nil if this position contains a nested array
}

// Restorer holds parsed field definitions and exposes RestoreFields.
type Restorer struct {
	fields map[string][]fieldDef
}

// NewFromFile creates a Restorer by reading definitions from a JSON file.
//
// The JSON format is:
//
//	{
//	  "userCards": ["cardId", "level", ..., ["episodes", ["cardEpisodeId", ...]]],
//	  ...
//	}
//
// A string element is a simple key. An array element [name, [...subkeys]]
// indicates a nested sub-array field.
func NewFromFile(path string) (*Restorer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read suite structure file: %w", err)
	}
	var raw map[string][]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse suite structure file: %w", err)
	}
	fields := make(map[string][]fieldDef, len(raw))
	for fieldName, defs := range raw {
		parsed, err := parseFieldDefs(defs)
		if err != nil {
			return nil, fmt.Errorf("parse field %s: %w", fieldName, err)
		}
		fields[fieldName] = parsed
	}
	return &Restorer{fields: fields}, nil
}

// parseFieldDefs converts a raw JSON array of mixed string/array elements
// into typed fieldDef slices.
func parseFieldDefs(raw []any) ([]fieldDef, error) {
	result := make([]fieldDef, 0, len(raw))
	for _, elem := range raw {
		switch v := elem.(type) {
		case string:
			result = append(result, fieldDef{Key: v})
		case []any:
			// Nested: ["fieldName", ["subKey1", "subKey2", ...]]
			if len(v) != 2 {
				return nil, fmt.Errorf("nested field def must have 2 elements, got %d", len(v))
			}
			name, ok := v[0].(string)
			if !ok {
				return nil, fmt.Errorf("nested field name must be string, got %T", v[0])
			}
			subRaw, ok := v[1].([]any)
			if !ok {
				return nil, fmt.Errorf("nested field keys must be array, got %T", v[1])
			}
			children, err := parseFieldDefs(subRaw)
			if err != nil {
				return nil, fmt.Errorf("nested field %s: %w", name, err)
			}
			result = append(result, fieldDef{Key: name, Children: children})
		default:
			return nil, fmt.Errorf("unexpected element type: %T", elem)
		}
	}
	return result, nil
}

// RestoreFields converts array-encoded suite fields to dict format in-place.
// Fields that are already in dict format are left untouched.
// Returns the same map for convenience.
func (r *Restorer) RestoreFields(data map[string]any) map[string]any {
	for field, defs := range r.fields {
		v, ok := data[field]
		if !ok {
			continue
		}
		items, ok := v.([]any)
		if !ok {
			continue
		}
		data[field] = restoreSlice(items, defs)
	}
	return data
}

// restoreSlice converts each []any element into map[string]any using the
// field definitions. Elements already in dict form are checked for nested
// fields that might still need restoration.
func restoreSlice(items []any, defs []fieldDef) []any {
	result := make([]any, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case []any:
			result = append(result, arrayToDict(v, defs))
		case map[string]any:
			// Already a dict — but check nested fields
			restoreNestedInDict(v, defs)
			result = append(result, v)
		default:
			result = append(result, item)
		}
	}
	return result
}

// arrayToDict converts a single positional array into a keyed map,
// handling nested sub-arrays recursively.
func arrayToDict(arr []any, defs []fieldDef) map[string]any {
	dict := make(map[string]any, len(defs))
	for i, def := range defs {
		if i >= len(arr) || arr[i] == nil {
			continue
		}
		if def.Children != nil {
			// This position contains a nested array of arrays
			if subItems, ok := arr[i].([]any); ok {
				dict[def.Key] = restoreSlice(subItems, def.Children)
			} else {
				dict[def.Key] = arr[i]
			}
		} else {
			dict[def.Key] = arr[i]
		}
	}
	return dict
}

// restoreNestedInDict checks an already-dict element for nested array
// fields that might still need restoration.
func restoreNestedInDict(m map[string]any, defs []fieldDef) {
	for _, def := range defs {
		if def.Children == nil {
			continue
		}
		v, ok := m[def.Key]
		if !ok {
			continue
		}
		if subItems, ok := v.([]any); ok {
			m[def.Key] = restoreSlice(subItems, def.Children)
		}
	}
}
