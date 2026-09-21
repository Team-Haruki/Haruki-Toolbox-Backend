package nuverserestore

import (
	"crypto/sha256"
	json "encoding/json/v2"
	"fmt"
	"os"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
)

type layout struct {
	fields []definition
	hash   string
}

// MysekaiRestorer owns immutable schemas loaded at startup. Nil and empty configuration disable restoration.
type MysekaiRestorer struct{ regions map[string]layout }

// NewMysekai accepts AVSC file paths per region. Files are read once; replacement takes
// effect on restart and changes the response cache fingerprint even at the same path.
func NewMysekai(paths map[string]string) (*MysekaiRestorer, error) {
	r := &MysekaiRestorer{regions: make(map[string]layout)}
	for region, path := range paths {
		switch region {
		case "cn", "tw", "kr", "jp", "en":
		default:
			return nil, fmt.Errorf("mysekai restore: unsupported region %q", region)
		}
		if strings.TrimSpace(path) == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("mysekai schema %s (%s): %w", region, path, err)
		}
		defs, err := parseSchema(raw)
		if err != nil {
			return nil, fmt.Errorf("mysekai schema %s (%s): %w", region, path, err)
		}
		r.regions[region] = layout{fields: defs, hash: fmt.Sprintf("%x", sha256.Sum256(raw))}
	}
	return r, nil
}

// Fingerprint identifies the loaded file content, not its path.
func (r *MysekaiRestorer) Fingerprint(server string) string {
	if r == nil {
		return ""
	}
	return r.regions[server].hash
}

type schemaReader struct {
	records    map[string]map[string]any
	namespaces map[string]string
}

func parseSchema(raw []byte) ([]definition, error) {
	var root any
	if err := json.Unmarshal(raw, &root, jsonvalue.Numbers); err != nil {
		return nil, err
	}
	reader := schemaReader{records: map[string]map[string]any{}, namespaces: map[string]string{}}
	if err := reader.collect(root, "", 0); err != nil {
		return nil, err
	}
	// Prefer SuiteUser's actual field reference; do not guess from a similarly named record.
	var target any
	ns := ""
	for name, record := range reader.records {
		if shortName(name) != "SuiteUser" {
			continue
		}
		fields, _ := record["fields"].([]any)
		for _, v := range fields {
			f, _ := v.(map[string]any)
			if f["name"] == HarvestMaps {
				if target != nil {
					return nil, fmt.Errorf("ambiguous SuiteUser harvest field")
				}
				a, _, err := reader.resolve(f["type"], reader.namespaces[name])
				if err != nil {
					return nil, err
				}
				array, ok := a.(map[string]any)
				if !ok || array["type"] != "array" {
					return nil, fmt.Errorf("harvest field must be an array")
				}
				target = array["items"]
				ns = reader.namespaces[name]
			}
		}
	}
	if target == nil {
		// Also accept a dedicated harvest record or schema list without SuiteUser.
		for name, record := range reader.records {
			if shortName(name) == "UserMysekaiHarvestMap" {
				if target != nil {
					return nil, fmt.Errorf("ambiguous harvest record")
				}
				target = record
				ns = reader.namespaces[name]
			}
		}
	}
	if target == nil {
		return nil, fmt.Errorf("schema has no %s definition", HarvestMaps)
	}
	return reader.compile(target, ns, 0)
}
func shortName(name string) string { i := strings.LastIndexByte(name, '.'); return name[i+1:] }
func fullName(record map[string]any, parent string) (string, string) {
	name, _ := record["name"].(string)
	ns := parent
	if explicit, ok := record["namespace"].(string); ok {
		ns = explicit
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name, name[:i]
	}
	if ns != "" {
		return ns + "." + name, ns
	}
	return name, ns
}
func (r *schemaReader) collect(value any, ns string, depth int) error {
	if depth > 64 {
		return fmt.Errorf("schema nesting exceeds 64")
	}
	switch v := value.(type) {
	case map[string]any:
		if v["type"] == "record" {
			name, next := fullName(v, ns)
			if name == "" {
				return fmt.Errorf("unnamed record")
			}
			if _, ok := r.records[name]; ok {
				return fmt.Errorf("duplicate record %s", name)
			}
			r.records[name] = v
			r.namespaces[name] = next
			ns = next
		}
		for _, child := range v {
			if err := r.collect(child, ns, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := r.collect(child, ns, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *schemaReader) resolve(value any, ns string) (any, bool, error) {
	nullable := false
	if union, ok := value.([]any); ok {
		if len(union) != 2 {
			return nil, false, fmt.Errorf("unsupported union")
		}
		if union[0] == "null" {
			value = union[1]
		} else if union[1] == "null" {
			value = union[0]
		} else {
			return nil, false, fmt.Errorf("expected nullable union")
		}
		nullable = true
	}
	if name, ok := value.(string); ok {
		if record, ok := r.records[ns+"."+name]; ok {
			return record, nullable, nil
		}
		if record, ok := r.records[name]; ok {
			return record, nullable, nil
		}
	}
	return value, nullable, nil
}
func (r *schemaReader) compile(value any, ns string, depth int) ([]definition, error) {
	if depth > 16 {
		return nil, fmt.Errorf("recursive or excessively nested harvest schema")
	}
	resolved, _, err := r.resolve(value, ns)
	if err != nil {
		return nil, err
	}
	record, ok := resolved.(map[string]any)
	if !ok || record["type"] != "record" {
		return nil, fmt.Errorf("unresolved harvest record %v", value)
	}
	_, ns = fullName(record, ns)
	fields, ok := record["fields"].([]any)
	if !ok || len(fields) == 0 {
		return nil, fmt.Errorf("empty harvest record")
	}
	defs := make([]definition, len(fields))
	names := map[string]bool{}
	for _, raw := range fields {
		f, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid field")
		}
		name, _ := f["name"].(string)
		key, ok := f["msgpack_key"].(jsonvalue.Number)
		if !ok {
			return nil, fmt.Errorf("%s: integer msgpack_key required", name)
		}
		index, err := key.Int64()
		if err != nil || index < 0 || index >= int64(len(fields)) {
			return nil, fmt.Errorf("%s: non-contiguous msgpack_key", name)
		}
		if name == "" || name == Extra || strings.ContainsAny(name, ".$") || names[name] || defs[index].name != "" {
			return nil, fmt.Errorf("invalid or duplicate harvest field %q", name)
		}
		names[name] = true
		typ, nullable, err := r.resolve(f["type"], ns)
		if err != nil {
			return nil, err
		}
		d := definition{name: name, nullable: nullable}
		if primitive, ok := typ.(string); ok {
			d.kind = primitive
		} else {
			array, ok := typ.(map[string]any)
			if !ok || array["type"] != "array" {
				return nil, fmt.Errorf("%s: unsupported field type", name)
			}
			d.kind = "array"
			d.children, err = r.compile(array["items"], ns, depth+1)
			if err != nil {
				return nil, err
			}
		}
		switch d.kind {
		case "int", "long", "string", "boolean", "float", "double", "array":
		default:
			return nil, fmt.Errorf("%s: unsupported field type %s", name, d.kind)
		}
		defs[index] = d
	}
	return defs, nil
}
