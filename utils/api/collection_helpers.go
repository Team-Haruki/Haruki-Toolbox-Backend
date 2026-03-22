package api

import (
	"slices"
	"strings"
)

func ArrayContains(arr []string, s string) bool {
	return slices.Contains(arr, s)
}

func StringContains(s, substr string) bool {
	return strings.Contains(s, substr)
}
