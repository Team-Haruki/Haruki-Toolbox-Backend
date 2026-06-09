package handler

import (
	"encoding/json"
	"fmt"
	harukiLogger "haruki-suite/utils/logger"
	"math"
	"strconv"
)

const maxSafeIntegerFloat64 = 1<<53 - 1

type ParsedGameUserID struct {
	Value   *int64
	RawType string
}

func convertToStatusCode(status any, logger *harukiLogger.Logger) int {
	switch v := status.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case json.Number:
		if i64, err := v.Int64(); err == nil {
			return int(i64)
		}
	default:
		logger.Debugf("unexpected httpStatus type: %T, value: %v", v, v)
	}
	return 0
}

func extractUserIDFromGameData(unpackedMap map[string]any, logger *harukiLogger.Logger) (ParsedGameUserID, error) {
	gameData, ok := unpackedMap["userGamedata"].(map[string]any)
	if !ok {
		return ParsedGameUserID{}, nil
	}
	userIDValue, ok := gameData["userId"]
	if !ok {
		return ParsedGameUserID{}, nil
	}
	value, err := convertToInt64Pointer(userIDValue, logger)
	return ParsedGameUserID{Value: value, RawType: fmt.Sprintf("%T", userIDValue)}, err
}

func convertToInt64Pointer(value any, logger *harukiLogger.Logger) (*int64, error) {
	switch v := value.(type) {
	case json.Number:
		if id64, err := v.Int64(); err == nil {
			if id64 < 0 {
				return nil, nil
			}
			return &id64, nil
		}
		if u64, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
			if u64 > math.MaxInt64 {
				return nil, fmt.Errorf("userId too large for int64: %s", v.String())
			}
			tmp := int64(u64)
			return &tmp, nil
		}
	case string:
		if u64, err := strconv.ParseUint(v, 10, 64); err == nil {
			if u64 > math.MaxInt64 {
				return nil, fmt.Errorf("userId too large for int64: %s", v)
			}
			tmp := int64(u64)
			return &tmp, nil
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || math.Trunc(v) != v {
			return nil, nil
		}
		if v > maxSafeIntegerFloat64 {
			return nil, fmt.Errorf("unsafe numeric userId: %.0f", v)
		}
		tmp := int64(v)
		return &tmp, nil
	case int64:
		if v < 0 {
			return nil, nil
		}
		return &v, nil
	case uint64:
		if v > math.MaxInt64 {
			return nil, fmt.Errorf("userId too large for int64: %d", v)
		}
		tmp := int64(v)
		return &tmp, nil
	default:
		logger.Debugf("userId raw type: %T, value: %v", v, v)
	}
	return nil, nil
}
