package handler

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func firstPresent(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			return value
		}
	}
	return nil
}

func mapStringAny(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		result := make(map[string]any, len(typed))
		for key, value := range typed {
			switch keyText := key.(type) {
			case string:
				result[keyText] = value
			case []byte:
				result[string(keyText)] = value
			}
		}
		return result, len(result) > 0
	default:
		return nil, false
	}
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	case []map[any]any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	default:
		return nil
	}
}

func cloneMap(item map[string]any) map[string]any {
	result := make(map[string]any, len(item))
	for key, value := range item {
		result[key] = value
	}
	return result
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return fmt.Sprint(value)
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		if typed > int64(math.MaxInt) || typed < int64(math.MinInt) {
			return 0
		}
		return int(typed)
	case uint:
		if uint64(typed) > uint64(math.MaxInt) {
			return 0
		}
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		if uint64(typed) > uint64(math.MaxInt) {
			return 0
		}
		return int(typed)
	case uint64:
		if typed > uint64(math.MaxInt) {
			return 0
		}
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			if n > int64(math.MaxInt) || n < int64(math.MinInt) {
				return 0
			}
			return int(n)
		}
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return n
	case []byte:
		n, _ := strconv.Atoi(strings.TrimSpace(string(typed)))
		return n
	}
	return 0
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return n
		}
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n
	}
	return time.Now().Unix()
}

func floatFromAny(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case float32:
		return float64(typed)
	case float64:
		return typed
	case json.Number:
		n, _ := typed.Float64()
		return n
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return n
	case []byte:
		n, _ := strconv.ParseFloat(strings.TrimSpace(string(typed)), 64)
		return n
	}
	return 0
}
