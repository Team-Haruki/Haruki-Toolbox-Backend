package data

import (
	"fmt"
	"math"
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	fieldID           = "_id"
	fieldIDString     = "_idString"
	fieldUserGamedata = "userGamedata"
	fieldUserID       = "userId"
	fieldUserIDString = "userIdString"
)

func NormalizeProviderResponse(value any) any {
	return normalizeProviderValue(value, "")
}

func normalizeProviderValue(value any, objectName string) any {
	switch typed := value.(type) {
	case bson.D:
		return normalizeProviderDocument(typed, objectName)
	case []bson.D:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, normalizeProviderValue(item, ""))
		}
		return items
	case bson.A:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, normalizeProviderValue(item, ""))
		}
		return items
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, normalizeProviderValue(item, ""))
		}
		return items
	case bson.M:
		return normalizeProviderMap(map[string]any(typed), objectName)
	case map[string]any:
		return normalizeProviderMap(typed, objectName)
	default:
		return value
	}
}

func normalizeProviderDocument(doc bson.D, objectName string) map[string]any {
	out := make(map[string]any, len(doc)+1)
	var userIDString string
	var hasUserIDString bool
	var idString string
	var hasIDString bool
	for _, elem := range doc {
		normalized := normalizeProviderValue(elem.Value, elem.Key)
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

func normalizeProviderMap(in map[string]any, objectName string) map[string]any {
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = normalizeProviderValue(value, key)
	}
	if objectName == fieldUserGamedata {
		if value, ok := in[fieldUserID]; ok {
			if s, ok := providerIDString(value); ok {
				out[fieldUserIDString] = s
			}
		}
	}
	if objectName == "" {
		if value, ok := in[fieldID]; ok {
			if s, ok := providerIDString(value); ok {
				out[fieldIDString] = s
			}
		}
	}
	return out
}

func providerIDString(value any) (string, bool) {
	switch typed := value.(type) {
	case int:
		return strconv.FormatInt(int64(typed), 10), true
	case int8:
		return strconv.FormatInt(int64(typed), 10), true
	case int16:
		return strconv.FormatInt(int64(typed), 10), true
	case int32:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	case float32:
		v := float64(typed)
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v {
			return "", false
		}
		return strconv.FormatFloat(v, 'f', 0, 32), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return "", false
		}
		return strconv.FormatFloat(typed, 'f', 0, 64), true
	case string:
		trimmed := typed
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	case fmt.Stringer:
		text := typed.String()
		return text, text != ""
	default:
		return "", false
	}
}
