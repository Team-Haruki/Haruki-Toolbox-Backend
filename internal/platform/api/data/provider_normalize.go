package data

import (
	json "encoding/json/v2"
	"fmt"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
	"maps"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	fieldID           = "_id"
	fieldIDString     = "_idString"
	fieldUserGamedata = "userGamedata"
	fieldUserID       = "userId"
	fieldUserIDString = "userIdString"
)

// NormalizeProviderResponse returns a read-only view with provider string fields.
// Unchanged maps and slices are shared with the input; changed branches are copied.
// It never mutates input data.
func NormalizeProviderResponse(value any) any {
	out, _ := normalizeProviderValue(value, "")
	return out
}

func normalizeProviderValue(value any, objectName string) (any, bool) {
	switch typed := value.(type) {
	case bson.D:
		return normalizeProviderDocument(typed, objectName), true
	case []bson.D:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i], _ = normalizeProviderValue(item, "")
		}
		return items, true
	case bson.A:
		out, _ := normalizeProviderArray([]any(typed))
		return out, true
	case []any:
		out, changed := normalizeProviderArray(typed)
		if !changed {
			return value, false
		}
		return out, true
	case bson.M:
		out, _ := normalizeProviderMap(map[string]any(typed), objectName)
		return out, true
	case map[string]any:
		return normalizeProviderMap(typed, objectName)
	default:
		return value, false
	}
}

func normalizeProviderArray(in []any) ([]any, bool) {
	if in == nil {
		return []any{}, true
	}
	out := in
	changed := false
	for i, value := range in {
		normalized, childChanged := normalizeProviderValue(value, "")
		if childChanged {
			if !changed {
				out = slices.Clone(in)
				changed = true
			}
			out[i] = normalized
		}
	}
	return out, changed
}

func normalizeProviderDocument(doc bson.D, objectName string) map[string]any {
	out := make(map[string]any, len(doc)+1)
	var userIDString string
	var hasUserIDString bool
	var idString string
	var hasIDString bool
	for _, elem := range doc {
		normalized, _ := normalizeProviderValue(elem.Value, elem.Key)
		out[elem.Key] = normalized
		if objectName == fieldUserGamedata && elem.Key == fieldUserID {
			if s, ok := providerIDString(elem.Value); ok {
				userIDString = s
				hasUserIDString = true
			}
		}
		if objectName == "" && elem.Key == fieldID {
			if s, ok := providerIDString(elem.Value); ok {
				idString = s
				hasIDString = true
			}
		}
	}
	if hasUserIDString {
		out[fieldUserIDString] = userIDString
	}
	if hasIDString {
		out[fieldIDString] = idString
	}
	return out
}

func normalizeProviderMap(in map[string]any, objectName string) (map[string]any, bool) {
	out := in
	changed := false
	if in == nil {
		out = make(map[string]any)
		changed = true
	}
	set := func(key string, value any) {
		if !changed {
			out = maps.Clone(in)
			changed = true
		}
		out[key] = value
	}
	for key, value := range in {
		normalized, childChanged := normalizeProviderValue(value, key)
		if childChanged {
			set(key, normalized)
		}
	}
	source, target := "", ""
	if objectName == fieldUserGamedata {
		source, target = fieldUserID, fieldUserIDString
	}
	if objectName == "" {
		source, target = fieldID, fieldIDString
	}
	if value, ok := in[source]; source != "" && ok {
		if derived, ok := providerIDString(value); ok {
			if current, ok := out[target].(string); !ok || current != derived {
				set(target, derived)
			}
		}
	}
	return out, changed
}

func providerIDString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, v != ""
	case float32:
		// MessagePack JSON projects float32 through float64; use the same representation.
		value = float64(v)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float64:
	case jsonvalue.Number:
		// Validate and retain the exact numeric token through the shared scalar policy.
	case fmt.Stringer:
		text := v.String()
		return text, text != ""
	default:
		return "", false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return jsonvalue.ScalarString(raw)
}
