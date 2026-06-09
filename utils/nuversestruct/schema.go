package nuversestruct

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// GenerateSuiteStructures converts a StructTool/Avro schema into the light
// suite structure format consumed by utils/suiterestore.
func GenerateSuiteStructures(schemaJSON []byte) (map[string][]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(schemaJSON))
	decoder.UseNumber()

	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse schema json: %w", err)
	}

	g := schemaGenerator{
		namedRecords: make(map[string]map[string]any),
	}
	g.collectNamedRecords(raw)

	structures := make(map[string][]any)
	if err := g.collectTopLevelStructures(raw, structures); err != nil {
		return nil, err
	}
	return structures, nil
}

// MarshalSuiteStructures renders generated definitions in deterministic JSON.
func MarshalSuiteStructures(structures map[string][]any) ([]byte, error) {
	keys := make([]string, 0, len(structures))
	for key := range structures {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b bytes.Buffer
	b.WriteString("{\n")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(",\n")
		}
		keyJSON, _ := json.Marshal(key)
		valJSON, err := json.MarshalIndent(structures[key], "    ", "    ")
		if err != nil {
			return nil, fmt.Errorf("marshal structure %s: %w", key, err)
		}
		b.WriteString("    ")
		b.Write(keyJSON)
		b.WriteString(": ")
		b.Write(valJSON)
	}
	b.WriteString("\n}\n")
	return b.Bytes(), nil
}

type schemaGenerator struct {
	namedRecords map[string]map[string]any
}

func (g *schemaGenerator) collectNamedRecords(raw any) {
	switch v := raw.(type) {
	case map[string]any:
		if isRecord(v) {
			if name, ok := stringValue(v["name"]); ok && name != "" {
				g.namedRecords[name] = v
			}
		}
		for _, child := range v {
			g.collectNamedRecords(child)
		}
	case []any:
		for _, child := range v {
			g.collectNamedRecords(child)
		}
	}
}

func (g *schemaGenerator) collectTopLevelStructures(raw any, structures map[string][]any) error {
	switch v := raw.(type) {
	case map[string]any:
		if isRecord(v) {
			return g.collectRecordFields(v, structures)
		}
		if fieldsRaw, ok := v["fields"]; ok {
			return g.collectTopLevelStructures(fieldsRaw, structures)
		}
		for _, child := range v {
			if err := g.collectTopLevelStructures(child, structures); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := g.collectTopLevelStructures(child, structures); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *schemaGenerator) collectRecordFields(record map[string]any, structures map[string][]any) error {
	fields := sortedFields(record["fields"])
	for _, field := range fields {
		name, ok := stringValue(field["name"])
		if !ok || name == "" {
			continue
		}
		itemRecord := g.arrayItemRecord(field["type"])
		if itemRecord == nil {
			continue
		}
		defs, err := g.fieldDefs(itemRecord)
		if err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
		if len(defs) > 0 {
			structures[name] = defs
		}
	}
	return nil
}

func (g *schemaGenerator) fieldDefs(record map[string]any) ([]any, error) {
	fields := sortedFields(record["fields"])
	defs := make([]any, 0, len(fields))
	for _, field := range fields {
		name, ok := stringValue(field["name"])
		if !ok || name == "" {
			continue
		}
		if child := g.arrayItemRecord(field["type"]); child != nil {
			children, err := g.fieldDefs(child)
			if err != nil {
				return nil, fmt.Errorf("nested field %s: %w", name, err)
			}
			defs = append(defs, []any{name, children})
			continue
		}
		defs = append(defs, name)
	}
	return defs, nil
}

func (g *schemaGenerator) arrayItemRecord(rawType any) map[string]any {
	t := unwrapNullable(rawType)
	typeMap, ok := t.(map[string]any)
	if !ok {
		return nil
	}

	if typ, _ := stringValue(typeMap["type"]); typ == "array" {
		return g.recordFromType(typeMap["items"])
	}
	return nil
}

func (g *schemaGenerator) recordFromType(raw any) map[string]any {
	raw = unwrapNullable(raw)
	switch v := raw.(type) {
	case map[string]any:
		if isRecord(v) {
			return v
		}
		if typ, ok := stringValue(v["type"]); ok {
			return g.recordFromType(typ)
		}
	case string:
		if record, ok := g.namedRecords[v]; ok {
			return record
		}
		if i := strings.LastIndex(v, "."); i >= 0 {
			return g.namedRecords[v[i+1:]]
		}
	}
	return nil
}

func sortedFields(raw any) []map[string]any {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	fields := make([]map[string]any, 0, len(arr))
	for _, elem := range arr {
		field, ok := elem.(map[string]any)
		if ok {
			fields = append(fields, field)
		}
	}
	sort.SliceStable(fields, func(i, j int) bool {
		ki, hasI := msgpackKey(fields[i])
		kj, hasJ := msgpackKey(fields[j])
		if hasI && hasJ && ki != kj {
			return ki < kj
		}
		if hasI != hasJ {
			return hasI
		}
		ni, _ := stringValue(fields[i]["name"])
		nj, _ := stringValue(fields[j]["name"])
		return ni < nj
	})
	return fields
}

func msgpackKey(field map[string]any) (int, bool) {
	for _, key := range []string{"msgpack_key", "msgpackKey"} {
		raw, ok := field[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case json.Number:
			n, err := v.Int64()
			return int(n), err == nil
		case float64:
			return int(v), true
		case string:
			n, err := strconv.Atoi(v)
			return n, err == nil
		}
	}
	return 0, false
}

func unwrapNullable(raw any) any {
	arr, ok := raw.([]any)
	if !ok {
		return raw
	}
	for _, elem := range arr {
		if s, ok := elem.(string); ok && s == "null" {
			continue
		}
		return elem
	}
	return raw
}

func isRecord(v map[string]any) bool {
	typ, ok := stringValue(v["type"])
	return ok && typ == "record"
}

func stringValue(raw any) (string, bool) {
	s, ok := raw.(string)
	return s, ok
}
