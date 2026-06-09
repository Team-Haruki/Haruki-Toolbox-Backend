package nuversestruct

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	harukiUtils "haruki-suite/utils"
	"haruki-suite/utils/orderedmsgpack"
	"haruki-suite/utils/sekai"
	"haruki-suite/utils/suiterestore"

	"github.com/iancoleman/orderedmap"
)

type CompareOptions struct {
	SampleMsgpackPath     string
	CurrentStructuresPath string
	SchemaPath            string
	InputFormat           string
	Server                harukiUtils.SupportedDataUploadServer
}

type CompareReport struct {
	CurrentStructuresPath   string        `json:"currentStructuresPath"`
	SchemaPath              string        `json:"schemaPath"`
	SampleMsgpackPath       string        `json:"sampleMsgpackPath"`
	ComparedTopLevelFields  int           `json:"comparedTopLevelFields"`
	AddedFields             []string      `json:"fieldAdded"`
	RemovedFields           []string      `json:"fieldRemoved"`
	TypeChanged             []FieldChange `json:"fieldTypeChanged"`
	RowCountChanged         []FieldChange `json:"rowCountChanged"`
	RestoreFailed           []FieldError  `json:"fieldRestoreFailed"`
	GeneratedStructureCount int           `json:"generatedStructureCount"`
}

type FieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type FieldError struct {
	Field string `json:"field"`
	Error string `json:"error"`
}

const (
	InputFormatMsgpack   = "msgpack"
	InputFormatRawUpload = "raw-upload"
)

func CompareSuiteRestore(options CompareOptions) (*CompareReport, error) {
	if options.SampleMsgpackPath == "" {
		return nil, fmt.Errorf("sample msgpack path is required")
	}
	if options.CurrentStructuresPath == "" {
		return nil, fmt.Errorf("current structures path is required")
	}
	if options.SchemaPath == "" {
		return nil, fmt.Errorf("schema path is required")
	}

	schemaBytes, err := os.ReadFile(options.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	generatedStructures, err := GenerateSuiteStructures(schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("generate structures: %w", err)
	}

	currentRestorer, err := suiterestore.NewFromFile(options.CurrentStructuresPath)
	if err != nil {
		return nil, fmt.Errorf("load current structures: %w", err)
	}

	generatedRestorer, generatedPath, err := restorerFromDefinitions(generatedStructures)
	if err != nil {
		return nil, err
	}
	defer os.Remove(generatedPath)

	sampleBytes, err := decodeSampleMsgpack(options)
	if err != nil {
		return nil, err
	}
	decoded, err := orderedmsgpack.MsgpackToOrderedMap(sampleBytes)
	if err != nil {
		return nil, fmt.Errorf("decode sample msgpack: %w", err)
	}

	currentData := orderedMapToPlainMap(decoded)
	generatedData := orderedMapToPlainMap(decoded)
	currentRestorer.RestoreFields(currentData)
	generatedRestorer.RestoreFields(generatedData)

	report := &CompareReport{
		CurrentStructuresPath:   options.CurrentStructuresPath,
		SchemaPath:              options.SchemaPath,
		SampleMsgpackPath:       options.SampleMsgpackPath,
		GeneratedStructureCount: len(generatedStructures),
	}
	report.compare(currentData, generatedData)
	return report, nil
}

func decodeSampleMsgpack(options CompareOptions) ([]byte, error) {
	sampleBytes, err := os.ReadFile(options.SampleMsgpackPath)
	if err != nil {
		return nil, fmt.Errorf("read sample: %w", err)
	}

	switch inputFormat := normalizeInputFormat(options.InputFormat); inputFormat {
	case InputFormatMsgpack:
		return sampleBytes, nil
	case InputFormatRawUpload:
		if options.Server == "" {
			return nil, fmt.Errorf("server is required when input format is raw-upload")
		}
		msgpackBytes, err := sekai.DecryptToMsgpack(sampleBytes, options.Server)
		if err != nil {
			return nil, fmt.Errorf("decrypt raw upload sample: %w", err)
		}
		return msgpackBytes, nil
	default:
		return nil, fmt.Errorf("unsupported input format: %s", inputFormat)
	}
}

func normalizeInputFormat(inputFormat string) string {
	if inputFormat == "" {
		return InputFormatMsgpack
	}
	return inputFormat
}

func (r *CompareReport) compare(current map[string]any, generated map[string]any) {
	r.ComparedTopLevelFields = len(unionMapKeys(current, generated))
	r.compareValue("", current, generated)
}

func (r *CompareReport) compareValue(path string, current any, generated any) {
	leftMap, leftIsMap := asMap(current)
	rightMap, rightIsMap := asMap(generated)
	if leftIsMap && rightIsMap {
		keys := unionMapKeys(leftMap, rightMap)
		for _, key := range keys {
			left, hasLeft := leftMap[key]
			right, hasRight := rightMap[key]
			childPath := joinPath(path, key)
			switch {
			case !hasLeft && hasRight:
				r.AddedFields = append(r.AddedFields, childPath)
			case hasLeft && !hasRight:
				r.RemovedFields = append(r.RemovedFields, childPath)
			default:
				r.compareValue(childPath, left, right)
			}
		}
		return
	}

	leftSlice, leftIsSlice := asSlice(current)
	rightSlice, rightIsSlice := asSlice(generated)
	if leftIsSlice && rightIsSlice {
		if len(leftSlice) != len(rightSlice) {
			r.RowCountChanged = append(r.RowCountChanged, FieldChange{
				Field:  path,
				Before: fmt.Sprintf("%d", len(leftSlice)),
				After:  fmt.Sprintf("%d", len(rightSlice)),
			})
		}
		if len(leftSlice) > 0 && len(rightSlice) > 0 {
			r.compareValue(path+"[]", leftSlice[0], rightSlice[0])
		}
		return
	}

	leftType := valueKind(current)
	rightType := valueKind(generated)
	if leftType != rightType {
		r.TypeChanged = append(r.TypeChanged, FieldChange{
			Field:  path,
			Before: leftType,
			After:  rightType,
		})
	}
}

func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case *orderedmap.OrderedMap:
		return orderedMapToPlainMap(m), true
	case orderedmap.OrderedMap:
		return orderedMapToPlainMap(&m), true
	default:
		return nil, false
	}
}

func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

func joinPath(parent string, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func (r *CompareReport) MarshalJSONDeterministic() ([]byte, error) {
	normalizeReport(r)
	return json.MarshalIndent(r, "", "  ")
}

func restorerFromDefinitions(definitions map[string][]any) (*suiterestore.Restorer, string, error) {
	data, err := MarshalSuiteStructures(definitions)
	if err != nil {
		return nil, "", err
	}
	f, err := os.CreateTemp("", "nuverse-generated-suite-structures-*.json")
	if err != nil {
		return nil, "", fmt.Errorf("create generated structures temp file: %w", err)
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, "", fmt.Errorf("write generated structures temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, "", fmt.Errorf("close generated structures temp file: %w", err)
	}
	restorer, err := suiterestore.NewFromFile(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, "", fmt.Errorf("load generated structures: %w", err)
	}
	return restorer, path, nil
}

func orderedMapToPlainMap(om *orderedmap.OrderedMap) map[string]any {
	out := make(map[string]any, len(om.Keys()))
	for _, key := range om.Keys() {
		val, _ := om.Get(key)
		out[key] = clonePlainValue(val)
	}
	return out
}

func clonePlainValue(raw any) any {
	switch v := raw.(type) {
	case *orderedmap.OrderedMap:
		return orderedMapToPlainMap(v)
	case orderedmap.OrderedMap:
		return orderedMapToPlainMap(&v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, val := range v {
			out[key] = clonePlainValue(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = clonePlainValue(val)
		}
		return out
	default:
		return v
	}
}

func unionMapKeys(left map[string]any, right map[string]any) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func valueKind(v any) string {
	switch v.(type) {
	case []any:
		return "array"
	case map[string]any, *orderedmap.OrderedMap, orderedmap.OrderedMap:
		return "object"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func normalizeReport(r *CompareReport) {
	sort.Strings(r.AddedFields)
	sort.Strings(r.RemovedFields)
	sort.Slice(r.TypeChanged, func(i, j int) bool {
		return r.TypeChanged[i].Field < r.TypeChanged[j].Field
	})
	sort.Slice(r.RowCountChanged, func(i, j int) bool {
		return r.RowCountChanged[i].Field < r.RowCountChanged[j].Field
	})
	sort.Slice(r.RestoreFailed, func(i, j int) bool {
		return r.RestoreFailed[i].Field < r.RestoreFailed[j].Field
	})
}

func MarshalGeneratedStructuresFromSchema(schemaJSON []byte) ([]byte, error) {
	structures, err := GenerateSuiteStructures(schemaJSON)
	if err != nil {
		return nil, err
	}
	return MarshalSuiteStructures(structures)
}
