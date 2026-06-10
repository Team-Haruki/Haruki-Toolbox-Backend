package adminstats

import (
	"sort"
)

func normalizeCategoryCounts(rows []groupedFieldCount) []categoryCount {
	out := make([]categoryCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryCount(row))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Key < out[j].Key
		}
		return out[i].Count > out[j].Count
	})

	return out
}

func normalizeMethodDataTypeCounts(rows []groupedMethodDataTypeCount) []methodDataTypeCount {
	out := make([]methodDataTypeCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, methodDataTypeCount(row))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			if out[i].UploadMethod == out[j].UploadMethod {
				return out[i].DataType < out[j].DataType
			}
			return out[i].UploadMethod < out[j].UploadMethod
		}
		return out[i].Count > out[j].Count
	})

	return out
}
