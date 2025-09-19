package utils

import (
	"fmt"
)

func RestoreCompactData(data map[string]interface{}) []map[string]interface{} {
	enumRaw, _ := data["__ENUM__"].(map[string]interface{})
	var columnLabels []string
	var columns [][]interface{}

	for key, value := range data {
		if key == "__ENUM__" {
			continue
		}
		columnLabels = append(columnLabels, key)

		if enumColumnRaw, ok := enumRaw[key]; ok {
			enumColumn, _ := enumColumnRaw.([]interface{})
			var columnValues []interface{}
			dataColumn, _ := value.([]interface{})
			for _, v := range dataColumn {
				if v == nil {
					columnValues = append(columnValues, nil)
				} else {
					index, ok := v.(int)
					if !ok {
						// 尝试 float64 转 int（JSON 解析后可能是 float64）
						if f, okf := v.(float64); okf {
							index = int(f)
						} else {
							index = 0
						}
					}
					if index >= 0 && index < len(enumColumn) {
						columnValues = append(columnValues, enumColumn[index])
					} else {
						columnValues = append(columnValues, nil)
					}
				}
			}
			columns = append(columns, columnValues)
		} else {
			// 普通列
			colSlice, _ := value.([]interface{})
			columns = append(columns, colSlice)
		}
	}

	numEntries := len(columns[0])
	for _, col := range columns {
		if len(col) < numEntries {
			numEntries = len(col)
		}
	}

	var result []map[string]interface{}
	for i := 0; i < numEntries; i++ {
		entry := map[string]interface{}{}
		for j, key := range columnLabels {
			entry[key] = columns[j][i]
		}
		result = append(result, entry)
	}

	return result
}

func GetValueFromResult(result map[string]interface{}, key string) []map[string]interface{} {
	if val, ok := result[key]; ok {
		// val 必须是 []map[string]interface{}，否则返回空
		if arr, ok2 := val.([]map[string]interface{}); ok2 {
			return arr
		}
		return []map[string]interface{}{}
	}

	// 尝试 compact 名
	if len(key) == 0 {
		return []map[string]interface{}{}
	}
	compactName := fmt.Sprintf("compact%s%s", string(key[0]-32), key[1:])
	if val, ok := result[compactName]; ok {
		if compactData, ok2 := val.(map[string]interface{}); ok2 {
			return RestoreCompactData(compactData)
		}
	}
	return []map[string]interface{}{}
}
