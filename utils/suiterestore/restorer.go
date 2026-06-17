// Package suiterestore converts suite user-data fields from compact array
// format to keyed dict (map) format.
package suiterestore

import (
	"fmt"
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

// RestoreReport describes one in-place suite restore run.
type RestoreReport struct {
	RestoredFields int
	FailedFields   []string
}

// NewFromDefinitions creates a Restorer from StructTool-derived field definitions.
func NewFromDefinitions(raw map[string][]any) (*Restorer, error) {
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
	r.RestoreFieldsWithReport(data)
	return data
}

// RestoreFieldsWithReport converts suite fields in-place and reports top-level
// fields restored or failed. Unknown, missing, and already keyed fields are not
// treated as failures.
func (r *Restorer) RestoreFieldsWithReport(data map[string]any) (map[string]any, RestoreReport) {
	report := RestoreReport{}
	for field, defs := range r.fields {
		v, ok := data[field]
		if !ok {
			continue
		}
		items, ok := v.([]any)
		if !ok {
			continue
		}
		restored, changed, err := restoreFieldSafelyWithChanged(items, defs)
		if err != nil {
			report.FailedFields = append(report.FailedFields, field)
			continue
		}
		data[field] = restored
		if changed {
			report.RestoredFields++
		}
	}
	return data, report
}

func restoreSliceChanged(items []any, defs []fieldDef) ([]any, bool) {
	result := make([]any, 0, len(items))
	changed := false
	for _, item := range items {
		switch v := item.(type) {
		case []any:
			result = append(result, arrayToDict(v, defs))
			changed = true
		case map[string]any:
			if restoreNestedInDictChanged(v, defs) {
				changed = true
			}
			result = append(result, v)
		default:
			result = append(result, item)
		}
	}
	return result, changed
}

func restoreFieldSafelyWithChanged(items []any, defs []fieldDef) (restored []any, changed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
			restored = nil
			changed = false
		}
	}()
	restored, changed = restoreSliceChanged(items, defs)
	return restored, changed, nil
}

// restoreSlice converts each []any element into map[string]any using the
// field definitions. Elements already in dict form are checked for nested
// fields that might still need restoration.
func restoreSlice(items []any, defs []fieldDef) []any {
	result, _ := restoreSliceChanged(items, defs)
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

func restoreNestedInDictChanged(m map[string]any, defs []fieldDef) bool {
	changed := false
	for _, def := range defs {
		if def.Children == nil {
			continue
		}
		v, ok := m[def.Key]
		if !ok {
			continue
		}
		if subItems, ok := v.([]any); ok {
			restored, nestedChanged := restoreSliceChanged(subItems, def.Children)
			m[def.Key] = restored
			if nestedChanged || len(subItems) > 0 {
				changed = true
			}
		}
	}
	return changed
}
