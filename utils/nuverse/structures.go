package nuverse

import (
	"fmt"
	"os"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/iancoleman/orderedmap"
)

func loadStructures(path string) (*orderedmap.OrderedMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	om := orderedmap.New()
	om.SetEscapeHTML(false)
	if err := sonic.Unmarshal(data, om); err != nil {
		return nil, err
	}
	return om, nil
}

func restoreStructuredData(
	key string,
	value any,
	structures *orderedmap.OrderedMap,
	masterData *orderedmap.OrderedMap,
) any {
	structDefVal, exists := structures.Get(key)
	if !exists {
		return value
	}

	arr, ok := value.([]interface{})
	if !ok {
		return value
	}

	def, ok := structDefVal.([]interface{})
	if !ok {
		return value
	}

	newArr := make([]*orderedmap.OrderedMap, 0, len(arr))
	for _, v := range arr {
		if subArr, ok := v.([]interface{}); ok {
			newArr = append(newArr, RestoreDict(subArr, def))
		}
	}

	// Keep original value when restore produced no valid rows but source had rows.
	if len(newArr) == 0 && len(arr) > 0 {
		return value
	}

	masterData.Set(key, newArr)
	return any(newArr)
}

func NuverseMasterRestorer(
	masterData *orderedmap.OrderedMap,
	nuverseStructureFilePath string,
) (*orderedmap.OrderedMap, error) {
	restoredCompactMaster := orderedmap.New()
	restoredCompactMaster.SetEscapeHTML(false)

	structures, err := loadStructures(nuverseStructureFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load nuverve master structure: %v", err)
	}

	restoredFromCompact := make(map[string]bool)
	masterDataKeys := masterData.Keys()

	for _, key := range masterDataKeys {
		value, _ := masterData.Get(key)
		if key == "" || !strings.HasPrefix(key, compactPrefix) {
			continue
		}
		if err := restoreCompactKey(restoredCompactMaster, masterData, restoredFromCompact, key, value); err != nil {
			return nil, err
		}
	}

	for _, key := range masterDataKeys {
		value, _ := masterData.Get(key)
		if key == "" || strings.HasPrefix(key, compactPrefix) || restoredFromCompact[key] {
			continue
		}
		if err := restoreNormalKey(restoredCompactMaster, masterData, structures, key, value); err != nil {
			return nil, err
		}
	}

	return restoredCompactMaster, nil
}

func restoreCompactKey(
	restoredCompactMaster *orderedmap.OrderedMap,
	masterData *orderedmap.OrderedMap,
	restoredFromCompact map[string]bool,
	key string,
	value any,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("error restoring key %s: %v", key, r)
		}
	}()

	restoredCompactMaster.Set(key, value)
	vOm, ok := value.(*orderedmap.OrderedMap)
	if !ok {
		return nil
	}

	data := RestoreCompactData(vOm)
	newKeyOriginal := strings.TrimPrefix(key, compactPrefix)
	if newKeyOriginal == "" {
		return nil
	}

	newKey := lowerFirstLetter(newKeyOriginal)
	structuredData := any(data)
	idKey := idMergeKeyFor(newKey)
	if idKey != "" {
		masterData.Set(newKey, data)
		handleIDMerging(newKey, structuredData, idKey, masterData)
		structuredData, _ = masterData.Get(newKey)
	}
	restoredCompactMaster.Set(newKey, structuredData)
	restoredFromCompact[newKey] = true
	return nil
}

func restoreNormalKey(
	restoredCompactMaster *orderedmap.OrderedMap,
	masterData *orderedmap.OrderedMap,
	structures *orderedmap.OrderedMap,
	key string,
	value any,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("error restoring key %s: %v", key, r)
		}
	}()

	idKey := idMergeKeyFor(key)
	restoredValue := restoreStructuredData(key, value, structures, masterData)
	handleIDMerging(key, restoredValue, idKey, masterData)
	finalValue, exists := masterData.Get(key)
	if exists {
		restoredCompactMaster.Set(key, finalValue)
		return nil
	}
	restoredCompactMaster.Set(key, restoredValue)
	return nil
}

func idMergeKeyFor(key string) string {
	if key == eventCardsKey {
		return eventCardIDField
	}
	return ""
}

func lowerFirstLetter(key string) string {
	if key == "" {
		return ""
	}
	return strings.ToLower(key[:1]) + key[1:]
}
