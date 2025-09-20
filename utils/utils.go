package utils

import "strings"

func ArrayContains(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}

func StringContains(s, substr string) bool {
	return strings.Contains(s, substr)
}
