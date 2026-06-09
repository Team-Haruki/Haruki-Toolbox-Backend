package data

import (
	"fmt"
	"haruki-suite/utils/compactrestore"
	harukiLogger "haruki-suite/utils/logger"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var userGamedataAllowedFields = []string{"userId", "name", "deck", "exp", "totalExp", "coin", "rank"}

func RestoreCompactData(data bson.D) []bson.D {
	var enumRaw bson.D
	for _, elem := range data {
		if elem.Key == compactrestore.EnumKey {
			switch m := elem.Value.(type) {
			case bson.D:
				enumRaw = m
			case bson.M:
				enumRaw = bsonMToD(m)
			case map[string]any:
				enumRaw = mapToD(m)
			default:
				harukiLogger.Warnf("RestoreCompactData: unknown type for __ENUM__: %T", elem.Value)
			}
			break
		}
	}
	columns, enumColumns := extractColumnsAndLabels(data, enumRaw)
	rows := compactrestore.RestoreColumns(columns, enumColumns, compactrestore.Options{
		InvalidEnumValue:    compactrestore.NullInvalidEnumValue,
		ParseFloatEnumIndex: true,
	})
	return buildResultEntries(rows)
}

func extractColumnsAndLabels(data bson.D, enumRaw bson.D) ([]compactrestore.Column, map[string][]any) {
	columns := make([]compactrestore.Column, 0, len(data))
	enumColumns := make(map[string][]any)
	for _, elem := range data {
		if elem.Key == compactrestore.EnumKey {
			continue
		}
		dataColumn := convertToInterfaceSlice(elem.Value)
		if enumRaw != nil {
			if enumColumnRaw, ok := valueFromBSOND(enumRaw, elem.Key); ok {
				enumColumns[elem.Key] = convertToInterfaceSlice(enumColumnRaw)
			}
		}
		columns = append(columns, compactrestore.Column{Key: elem.Key, Values: dataColumn})
	}
	return columns, enumColumns
}

func convertToInterfaceSlice(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case bson.A:
		return v
	default:
		return []any{}
	}
}

func buildResultEntries(rows []compactrestore.Row) []bson.D {
	result := make([]bson.D, 0, len(rows))
	for _, row := range rows {
		entry := make(bson.D, 0, len(row))
		for _, field := range row {
			entry = append(entry, bson.E{Key: field.Key, Value: field.Value})
		}
		result = append(result, entry)
	}
	return result
}

func valueFromBSOND(d bson.D, key string) (any, bool) {
	for _, elem := range d {
		if elem.Key == key {
			return elem.Value, true
		}
	}
	return nil, false
}

func GetValueFromResult(result bson.D, key string) any {
	for _, elem := range result {
		if elem.Key == key {
			return elem.Value
		}
	}
	if len(key) == 0 {
		return bson.A{}
	}
	compactName := fmt.Sprintf("compact%s%s", string(key[0]-32), key[1:])
	for _, elem := range result {
		if elem.Key == compactName {
			switch m := elem.Value.(type) {
			case bson.D:
				return RestoreCompactData(m)
			case bson.M:
				return RestoreCompactData(bsonMToD(m))
			case map[string]any:
				return RestoreCompactData(mapToD(m))
			default:
				return bson.A{}
			}
		}
	}
	return bson.A{}
}

func BSONDToMap(d bson.D) map[string]any {
	result := make(map[string]any, len(d))
	for _, elem := range d {
		result[elem.Key] = normalizeBSONValue(elem.Value)
	}
	return result
}

func normalizeBSONValue(value any) any {
	switch v := value.(type) {
	case bson.D:
		return BSONDToMap(v)
	case []bson.D:
		items := make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, BSONDToMap(item))
		}
		return items
	case bson.A:
		items := make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, normalizeBSONValue(item))
		}
		return items
	case []any:
		items := make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, normalizeBSONValue(item))
		}
		return items
	case bson.M:
		result := make(map[string]any, len(v))
		for key, item := range v {
			result[key] = normalizeBSONValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			result[key] = normalizeBSONValue(item)
		}
		return result
	default:
		return v
	}
}

func bsonMToD(m bson.M) bson.D {
	d := make(bson.D, 0, len(m))
	for k, v := range m {
		d = append(d, bson.E{Key: k, Value: v})
	}
	return d
}

func mapToD(m map[string]any) bson.D {
	d := make(bson.D, 0, len(m))
	for k, v := range m {
		d = append(d, bson.E{Key: k, Value: v})
	}
	return d
}
