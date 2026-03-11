package nuverse

import (
	"github.com/iancoleman/orderedmap"
)

func extractTupleKeys(v interface{}) []interface{} {
	switch s := v.(type) {
	case *orderedmap.OrderedMap:
		if tupleKeysRaw, found := s.Get(tupleKey); found {
			if keys, ok := tupleKeysRaw.([]interface{}); ok {
				return keys
			}
		}
	case orderedmap.OrderedMap:
		if tupleKeysRaw, found := s.Get(tupleKey); found {
			if keys, ok := tupleKeysRaw.([]interface{}); ok {
				return keys
			}
		}
	case map[string]interface{}:
		if tupleKeysRaw, found := s[tupleKey]; found {
			if keys, ok := tupleKeysRaw.([]interface{}); ok {
				return keys
			}
		}
	}
	return nil
}

func buildDictFromTuple(tupleKeys []interface{}, tupleVals []interface{}) *orderedmap.OrderedMap {
	dict := orderedmap.New()
	dict.SetEscapeHTML(false)

	for j, v := range tupleVals {
		if j >= len(tupleKeys) {
			break
		}
		if v == nil {
			continue
		}
		if keyStr, ok := tupleKeys[j].(string); ok {
			dict.Set(keyStr, v)
		}
	}
	return dict
}

func handleSimpleTuple(keyStructure []interface{}, arrayData []interface{}) (*orderedmap.OrderedMap, bool) {
	if len(keyStructure) != 2 {
		return nil, false
	}

	keyName, ok := keyStructure[0].(string)
	if !ok {
		return nil, false
	}

	tupleKeys := extractTupleKeys(keyStructure[1])
	if tupleKeys == nil {
		return nil, false
	}

	tupleVals := arrayData
	if len(arrayData) == 1 {
		if innerArr, ok := arrayData[0].([]interface{}); ok {
			tupleVals = innerArr
		}
	}

	result := orderedmap.New()
	result.SetEscapeHTML(false)
	dict := buildDictFromTuple(tupleKeys, tupleVals)
	result.Set(keyName, dict)
	return result, true
}

func processTupleField(second interface{}, arrayData []interface{}, i int) *orderedmap.OrderedMap {
	tupleKeys := extractTupleKeys(second)
	if tupleKeys == nil {
		return nil
	}

	var tupleVals []interface{}
	if i < len(arrayData) && arrayData[i] != nil {
		tupleVals, _ = arrayData[i].([]interface{})
	}

	if tupleVals == nil {
		return nil
	}

	return buildDictFromTuple(tupleKeys, tupleVals)
}

func processNestedArray(
	arrayData []interface{},
	i int,
	second []interface{},
) []*orderedmap.OrderedMap {
	subList := make([]*orderedmap.OrderedMap, 0)
	if i >= len(arrayData) {
		return subList
	}

	arr, ok := arrayData[i].([]interface{})
	if !ok {
		return subList
	}

	for _, sub := range arr {
		subArr, ok := sub.([]interface{})
		if !ok {
			continue
		}

		if len(second) > 0 {
			if innerStruct, ok := second[0].([]interface{}); ok && len(innerStruct) >= 2 {
				subList = append(subList, RestoreDict(subArr, innerStruct))
			} else {
				subList = append(subList, RestoreDict(subArr, second))
			}
		} else {
			subList = append(subList, RestoreDict(subArr, second))
		}
	}
	return subList
}

func RestoreDict(arrayData []interface{}, keyStructure []interface{}) *orderedmap.OrderedMap {
	result := orderedmap.New()
	result.SetEscapeHTML(false)
	if simpleResult, ok := handleSimpleTuple(keyStructure, arrayData); ok {
		return simpleResult
	}
	for i, key := range keyStructure {
		switch k := key.(type) {
		case []interface{}:
			if len(k) < 2 {
				continue
			}
			keyName, ok := k[0].(string)
			if !ok {
				continue
			}

			switch second := k[1].(type) {
			case *orderedmap.OrderedMap, orderedmap.OrderedMap, map[string]interface{}:
				if dict := processTupleField(second, arrayData, i); dict != nil {
					result.Set(keyName, dict)
				}

			case []interface{}:
				subList := processNestedArray(arrayData, i, second)
				result.Set(keyName, subList)
			}

		case string:
			if i < len(arrayData) && arrayData[i] != nil {
				result.Set(k, arrayData[i])
			}
		}
	}
	return result
}
