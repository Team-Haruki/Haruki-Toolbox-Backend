package handler

import (
	"encoding/json"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
)

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

func extractUserIDFromGameData(unpackedMap map[string]any, logger *harukiLogger.Logger) *int64 {
	gameData, ok := unpackedMap["userGamedata"].(map[string]any)
	if !ok {
		return nil
	}
	userIDValue, ok := gameData["userId"]
	if !ok {
		return nil
	}
	return convertToInt64Pointer(userIDValue, logger)
}

func convertToInt64Pointer(value any, logger *harukiLogger.Logger) *int64 {
	switch v := value.(type) {
	case json.Number:
		if id64, err := v.Int64(); err == nil {
			return &id64
		}
		if u64, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
			tmp := int64(u64)
			return &tmp
		}
	case string:
		if u64, err := strconv.ParseUint(v, 10, 64); err == nil {
			tmp := int64(u64)
			return &tmp
		}
	case float64:
		tmp := int64(v)
		return &tmp
	case int64:
		return &v
	case uint64:
		tmp := int64(v)
		return &tmp
	default:
		logger.Debugf("userId raw type: %T, value: %v", v, v)
	}
	return nil
}
