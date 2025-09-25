package utils

import (
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
)

func RestoreCompactData(data bson.M) []map[string]interface{} {
	enumRaw, _ := data["__ENUM__"].(bson.M)

	var columnLabels []string
	var columns [][]interface{}

	for key, value := range data {
		if key == "__ENUM__" {
			continue
		}
		columnLabels = append(columnLabels, key)

		var dataColumn []interface{}
		switch v := value.(type) {
		case []interface{}:
			dataColumn = v
		case bson.A:
			dataColumn = v
		default:
			dataColumn = []interface{}{}
		}

		if enumRaw != nil {
			if enumColumnRaw, ok := enumRaw[key]; ok {
				var enumSlice []interface{}
				switch e := enumColumnRaw.(type) {
				case []interface{}:
					enumSlice = e
				case bson.A:
					enumSlice = e
				default:
					enumSlice = nil
				}

				columnValues := make([]interface{}, 0, len(dataColumn))
				for _, v := range dataColumn {
					if v == nil {
						columnValues = append(columnValues, nil)
						continue
					}
					var index int
					switch t := v.(type) {
					case int:
						index = t
					case int32:
						index = int(t)
					case int64:
						index = int(t)
					case float64:
						index = int(t)
					default:
						index = 0
					}
					if index >= 0 && index < len(enumSlice) {
						columnValues = append(columnValues, enumSlice[index])
					} else {
						columnValues = append(columnValues, nil)
					}
				}
				columns = append(columns, columnValues)
				continue
			}
		}

		columns = append(columns, dataColumn)
	}

	if len(columns) == 0 {
		return []map[string]interface{}{}
	}

	numEntries := len(columns[0])
	for _, col := range columns {
		if len(col) < numEntries {
			numEntries = len(col)
		}
	}

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
