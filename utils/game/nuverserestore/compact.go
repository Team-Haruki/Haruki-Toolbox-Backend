package nuverserestore

import (
	"strconv"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"
)

const EnumKey = "__ENUM__"

type InvalidEnumValueMode int

const (
	NullInvalidEnumValue InvalidEnumValueMode = iota
	PreserveInvalidEnumValue
)

type CompactOptions struct {
	InvalidEnumValue       InvalidEnumValueMode
	ParseStringEnumIndex   bool
	ParseFloatEnumIndex    bool
	ParseUnsignedEnumIndex bool
}

type CompactColumn struct {
	Key    string
	Values []any
}

type CompactField struct {
	Key   string
	Value any
}

type CompactRow []CompactField

func RestoreColumns(columns []CompactColumn, enumColumns map[string][]any, options CompactOptions) []CompactRow {
	if len(columns) == 0 {
		return []CompactRow{}
	}

	restoredColumns := make([]CompactColumn, 0, len(columns))
	for _, column := range columns {
		if enumValues, ok := enumColumns[column.Key]; ok {
			column.Values = RestoreEnumColumn(column.Values, enumValues, options)
		}
		restoredColumns = append(restoredColumns, column)
	}

	numEntries := len(restoredColumns[0].Values)
	for _, column := range restoredColumns {
		if len(column.Values) < numEntries {
			numEntries = len(column.Values)
		}
	}

	result := make([]CompactRow, 0, numEntries)
	for i := 0; i < numEntries; i++ {
		entry := make(CompactRow, 0, len(restoredColumns))
		for _, column := range restoredColumns {
			var val any
			if i < len(column.Values) {
				val = column.Values[i]
			}
			entry = append(entry, CompactField{Key: column.Key, Value: val})
		}
		result = append(result, entry)
	}
	return result
}

func RestoreEnumColumn(dataColumn []any, enumValues []any, options CompactOptions) []any {
	mapped := make([]any, 0, len(dataColumn))
	for _, value := range dataColumn {
		mapped = append(mapped, mapEnumValue(value, enumValues, options))
	}
	return mapped
}

func mapEnumValue(value any, enumValues []any, options CompactOptions) any {
	if value == nil {
		return nil
	}
	idx, ok := enumIndex(value, options)
	if ok && idx >= 0 && idx < len(enumValues) {
		return enumValues[idx]
	}
	if options.InvalidEnumValue == PreserveInvalidEnumValue {
		return value
	}
	return nil
}

func enumIndex(value any, options CompactOptions) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		if options.ParseUnsignedEnumIndex {
			return int(v), true
		}
	case uint8:
		if options.ParseUnsignedEnumIndex {
			return int(v), true
		}
	case uint16:
		if options.ParseUnsignedEnumIndex {
			return int(v), true
		}
	case uint32:
		if options.ParseUnsignedEnumIndex {
			return int(v), true
		}
	case uint64:
		if options.ParseUnsignedEnumIndex {
			return int(v), true
		}
	case float32:
		if options.ParseFloatEnumIndex {
			return int(v), true
		}
	case float64:
		if options.ParseFloatEnumIndex {
			return int(v), true
		}
	case string:
		if options.ParseStringEnumIndex {
			idx, err := strconv.Atoi(v)
			return idx, err == nil
		}
	case jsonvalue.Number:
		if options.ParseStringEnumIndex {
			idx, err := strconv.Atoi(string(v))
			return idx, err == nil
		}
	}
	return 0, false
}
