package public

import (
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
)

func RestoreCompactData(data bson.M) []map[string]interface{} {
	enumRaw, _ := data["__ENUM__"].(bson.M)

	columnLabels, columns := extractColumnsAndLabels(data, enumRaw)

	if len(columns) == 0 {
		return []map[string]interface{}{}
	}

	numEntries := calculateMinEntries(columns)
	return buildResultEntries(numEntries, columnLabels, columns)
}

func extractColumnsAndLabels(data bson.M, enumRaw bson.M) ([]string, [][]interface{}) {
	var columnLabels []string
	var columns [][]interface{}

	for key, value := range data {
		if key == "__ENUM__" {
			continue
		}
		columnLabels = append(columnLabels, key)

		dataColumn := convertToInterfaceSlice(value)

		if enumRaw != nil {
			if enumColumn := processEnumColumn(enumRaw, key, dataColumn); enumColumn != nil {
				columns = append(columns, enumColumn)
				continue
			}
		}

		columns = append(columns, dataColumn)
	}

	return columnLabels, columns
}

func convertToInterfaceSlice(value interface{}) []interface{} {
	switch v := value.(type) {
	case []interface{}:
		return v
	case bson.A:
		return v
	default:
		return []interface{}{}
	}
}

func processEnumColumn(enumRaw bson.M, key string, dataColumn []interface{}) []interface{} {
	enumColumnRaw, ok := enumRaw[key]
	if !ok {
		return nil
	}

	enumSlice := convertToInterfaceSlice(enumColumnRaw)
	if enumSlice == nil {
		return nil
	}

	columnValues := make([]interface{}, 0, len(dataColumn))
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

func convertToInt(v interface{}) int {
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

func calculateMinEntries(columns [][]interface{}) int {
	numEntries := len(columns[0])
	for _, col := range columns {
		if len(col) < numEntries {
			numEntries = len(col)
		}
	}
	return numEntries
}

func buildResultEntries(numEntries int, columnLabels []string, columns [][]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, numEntries)
	for i := 0; i < numEntries; i++ {
		entry := make(map[string]interface{}, len(columnLabels))
		for j, key := range columnLabels {
			if i < len(columns[j]) {
				entry[key] = columns[j][i]
			} else {
				entry[key] = nil
			}
		}
		result = append(result, entry)
	}

	return result
}

func GetValueFromResult(result bson.M, key string) interface{} {
	if val, ok := result[key]; ok {
		return val
	}
	if len(key) == 0 {
		return []interface{}{}
	}
	compactName := fmt.Sprintf("compact%s%s", string(key[0]-32), key[1:])
	val, ok := result[compactName]
	if !ok {
		return []interface{}{}
	}

	switch m := val.(type) {
	case bson.M:
		return RestoreCompactData(m)
	case map[string]interface{}:
		return RestoreCompactData(m)
	case bson.D:
		bm := make(bson.M, len(m))
		for _, elem := range m {
			bm[elem.Key] = elem.Value
		}
		return RestoreCompactData(bm)
	default:
		return []interface{}{}
	}
}
