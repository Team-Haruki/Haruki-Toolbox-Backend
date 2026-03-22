package nuverse

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/iancoleman/orderedmap"
)

func extractEnumOM(data *orderedmap.OrderedMap) *orderedmap.OrderedMap {
	v, ok := data.Get(enumKey)
	if !ok {
		return nil
	}

	switch em := v.(type) {
	case *orderedmap.OrderedMap:
		return em
	case map[string]any:
		om := orderedmap.New()
		om.SetEscapeHTML(false)
		keys := make([]string, 0, len(em))
		for k := range em {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			om.Set(k, em[k])
		}
		return om
	}
	return nil
}

func convertEnumToSlice(e *orderedmap.OrderedMap) []interface{} {
	keys := e.Keys()
	allNum := true
	idx := make([]int, len(keys))

	for i, k := range keys {
		n, err := strconv.Atoi(k)
		if err != nil {
			allNum = false
			break
		}
		idx[i] = n
	}

	if allNum {
		return convertNumericEnumToSlice(e, keys, idx)
	}
	return convertStringEnumToSlice(e, keys)
}

func convertNumericEnumToSlice(
	e *orderedmap.OrderedMap,
	keys []string,
	idx []int,
) []interface{} {
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return idx[order[i]] < idx[order[j]] })

	kMax := -1
	for _, n := range idx {
		if n > kMax {
			kMax = n
		}
	}

	enumSlice := make([]interface{}, kMax+1)
	for _, oi := range order {
		k := keys[oi]
		v, _ := e.Get(k)
		n := idx[oi]
		if n >= 0 && n < len(enumSlice) {
			enumSlice[n] = v
		}
	}
	return enumSlice
}

func convertStringEnumToSlice(e *orderedmap.OrderedMap, keys []string) []interface{} {
	enumSlice := make([]interface{}, 0, len(keys))
	for _, k := range keys {
		v, _ := e.Get(k)
		enumSlice = append(enumSlice, v)
	}
	return enumSlice
}

func convertIntType(v interface{}) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int8:
		return int(t), true
	case int16:
		return int(t), true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case uint:
		return int(t), true
	case uint8:
		return int(t), true
	case uint16:
		return int(t), true
	case uint32:
		return int(t), true
	case uint64:
		return int(t), true
	}
	return 0, false
}

func convertFloatType(v interface{}) (int, bool) {
	switch t := v.(type) {
	case float32:
		return int(t), true
	case float64:
		return int(t), true
	}
	return 0, false
}

func convertStringType(v interface{}) (int, bool) {
	switch t := v.(type) {
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n, true
		}
	case json.Number:
		if n, err := strconv.Atoi(string(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

func convertValueToIndex(v interface{}) int {
	if idx, ok := convertIntType(v); ok {
		return idx
	}
	if idx, ok := convertFloatType(v); ok {
		return idx
	}
	if idx, ok := convertStringType(v); ok {
		return idx
	}
	return -1
}

func mapEnumValue(v interface{}, enumSlice []interface{}) interface{} {
	if v == nil {
		return nil
	}

	idx := convertValueToIndex(v)
	if idx >= 0 && idx < len(enumSlice) {
		return enumSlice[idx]
	}
	return v
}

func processEnumColumn(dataColumn []interface{}, enumColRaw interface{}) []interface{} {
	var enumSlice []interface{}

	switch e := enumColRaw.(type) {
	case []interface{}:
		enumSlice = e
	case *orderedmap.OrderedMap:
		enumSlice = convertEnumToSlice(e)
	}

	if enumSlice == nil {
		return dataColumn
	}

	mapped := make([]interface{}, len(dataColumn))
	for i, v := range dataColumn {
		mapped[i] = mapEnumValue(v, enumSlice)
	}
	return mapped
}

func RestoreCompactData(data *orderedmap.OrderedMap) []*orderedmap.OrderedMap {
	var (
		columnLabels []string
		columns      [][]interface{}
	)

	enumOM := extractEnumOM(data)

	for _, key := range data.Keys() {
		if key == enumKey {
			continue
		}
		columnLabels = append(columnLabels, key)

		var dataColumn []interface{}
		if val, ok := data.Get(key); ok {
			if vSlice, ok := val.([]interface{}); ok {
				dataColumn = vSlice
			} else {
				dataColumn = []interface{}{}
			}
		} else {
			dataColumn = []interface{}{}
		}

		if enumOM != nil {
			if enumColRaw, ok := enumOM.Get(key); ok {
				dataColumn = processEnumColumn(dataColumn, enumColRaw)
			}
		}

		columns = append(columns, dataColumn)
	}

	if len(columns) == 0 {
		return []*orderedmap.OrderedMap{}
	}

	numEntries := len(columns[0])
	for _, col := range columns {
		if len(col) < numEntries {
			numEntries = len(col)
		}
	}

	result := make([]*orderedmap.OrderedMap, 0, numEntries)
	for i := 0; i < numEntries; i++ {
		entry := orderedmap.New()
		entry.SetEscapeHTML(false)
		for j, key := range columnLabels {
			if i < len(columns[j]) {
				entry.Set(key, columns[j][i])
			} else {
				entry.Set(key, nil)
			}
		}
		result = append(result, entry)
	}
	return result
}
