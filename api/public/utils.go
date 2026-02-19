package public

import (
	"fmt"
	harukiLogger "haruki-suite/utils/logger"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var userGamedataAllowedFields = []string{"userId", "name", "deck", "exp", "totalExp", "coin"}

func RestoreCompactData(data bson.D) []bson.D {
	var enumRaw bson.D
	for _, elem := range data {
		if elem.Key == "__ENUM__" {
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
	columnLabels, columns := extractColumnsAndLabels(data, enumRaw)
	if len(columns) == 0 {
		return []bson.D{}
	}
	numEntries := calculateMinEntries(columns)
	return buildResultEntries(numEntries, columnLabels, columns)
}

func extractColumnsAndLabels(data bson.D, enumRaw bson.D) ([]string, [][]any) {
	var columnLabels []string
	var columns [][]any
	for _, elem := range data {
		if elem.Key == "__ENUM__" {
			continue
		}
		columnLabels = append(columnLabels, elem.Key)
		dataColumn := convertToInterfaceSlice(elem.Value)
		if enumRaw != nil {
			if enumColumn := processEnumColumn(enumRaw, elem.Key, dataColumn); enumColumn != nil {
				columns = append(columns, enumColumn)
				continue
			}
		}
		columns = append(columns, dataColumn)
	}
	return columnLabels, columns
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

func processEnumColumn(enumRaw bson.D, key string, dataColumn []any) []any {
	var enumColumnRaw any
	found := false
	for _, elem := range enumRaw {
		if elem.Key == key {
			enumColumnRaw = elem.Value
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	enumSlice := convertToInterfaceSlice(enumColumnRaw)
	if enumSlice == nil {
		return nil
	}
	columnValues := make([]any, 0, len(dataColumn))
	for _, v := range dataColumn {
		if v == nil {
			columnValues = append(columnValues, nil)
			continue
		}
		index := convertToInt(v)
		if index >= 0 && index < len(enumSlice) {
			columnValues = append(columnValues, enumSlice[index])
		} else {
			columnValues = append(columnValues, nil)
		}
	}
	return columnValues
}

func convertToInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func calculateMinEntries(columns [][]any) int {
	numEntries := len(columns[0])
	for _, col := range columns {
		if len(col) < numEntries {
			numEntries = len(col)
		}
	}
	return numEntries
}

func buildResultEntries(numEntries int, columnLabels []string, columns [][]any) []bson.D {
	result := make([]bson.D, 0, numEntries)
	for i := range numEntries {
		entry := make(bson.D, 0, len(columnLabels))
		for j, key := range columnLabels {
			var val any
			if i < len(columns[j]) {
				val = columns[j][i]
			}
			entry = append(entry, bson.E{Key: key, Value: val})
		}
		result = append(result, entry)
	}
	return result
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
