package nuverse

import (
	"sort"
	"strconv"

	"haruki-suite/utils/compactrestore"

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

func enumValuesFromRaw(enumColRaw interface{}) []any {
	switch e := enumColRaw.(type) {
	case []interface{}:
		return e
	case *orderedmap.OrderedMap:
		return convertEnumToSlice(e)
	}
	return nil
}

func RestoreCompactData(data *orderedmap.OrderedMap) []*orderedmap.OrderedMap {
	enumOM := extractEnumOM(data)
	columns := make([]compactrestore.Column, 0, len(data.Keys()))
	enumColumns := make(map[string][]any)

	for _, key := range data.Keys() {
		if key == enumKey {
			continue
		}

		var dataColumn []any
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
				if enumValues := enumValuesFromRaw(enumColRaw); enumValues != nil {
					enumColumns[key] = enumValues
				}
			}
		}

		columns = append(columns, compactrestore.Column{Key: key, Values: dataColumn})
	}

	rows := compactrestore.RestoreColumns(columns, enumColumns, compactrestore.Options{
		InvalidEnumValue:       compactrestore.PreserveInvalidEnumValue,
		ParseStringEnumIndex:   true,
		ParseFloatEnumIndex:    true,
		ParseUnsignedEnumIndex: true,
	})
	result := make([]*orderedmap.OrderedMap, 0, len(rows))
	for _, row := range rows {
		entry := orderedmap.New()
		entry.SetEscapeHTML(false)
		for _, field := range row {
			entry.Set(field.Key, field.Value)
		}
		result = append(result, entry)
	}
	return result
}
