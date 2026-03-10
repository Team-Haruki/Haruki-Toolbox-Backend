package nuverse

import (
	"sort"

	"github.com/iancoleman/orderedmap"
)

func extractValueIDs(arr []*orderedmap.OrderedMap, idKey string) map[any]bool {
	valueIDs := make(map[any]bool, len(arr))
	for _, item := range arr {
		if id, ok := item.Get(idKey); ok {
			valueIDs[id] = true
		}
	}
	return valueIDs
}

func convertToOrderedMap(x any) *orderedmap.OrderedMap {
	switch t := x.(type) {
	case *orderedmap.OrderedMap:
		return t
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		om := orderedmap.New()
		om.SetEscapeHTML(false)
		for _, k2 := range keys {
			om.Set(k2, t[k2])
		}
		return om
	}
	return nil
}

func mergeAndSortData(
	value any,
	arr []*orderedmap.OrderedMap,
	valueIDs map[any]bool,
	idKey string,
) []*orderedmap.OrderedMap {
	merged := make([]*orderedmap.OrderedMap, 0)
	if vs, ok := value.([]interface{}); ok {
		for _, x := range vs {
			m := convertToOrderedMap(x)
			if m == nil {
				continue
			}
			if id, ok := m.Get(idKey); ok && !valueIDs[id] {
				merged = append(merged, m)
			}
		}
	}
	merged = append(merged, arr...)
	sort.SliceStable(merged, func(i, j int) bool {
		vi, _ := merged[i].Get(idKey)
		vj, _ := merged[j].Get(idKey)
		return toInt64(vi) < toInt64(vj)
	})
	return merged
}

func handleIDMerging(key string, value any, idKey string, masterData *orderedmap.OrderedMap) {
	if idKey == "" {
		return
	}

	arrAny, _ := masterData.Get(key)
	var arr []*orderedmap.OrderedMap
	if a, ok := arrAny.([]*orderedmap.OrderedMap); ok {
		arr = a
	}
	if len(arr) == 0 {
		return
	}

	valueIDs := extractValueIDs(arr, idKey)
	merged := mergeAndSortData(value, arr, valueIDs, idKey)
	masterData.Set(key, merged)
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		if t > ^uint64(0)>>1 {
			return int64(^uint64(0) >> 1)
		}
		return int64(t)
	case float64:
		return int64(t)
	default:
		return 0
	}
}
