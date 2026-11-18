package config

import "strings"

// IsExampleCredential identifies placeholders in the shipped configuration.
func IsExampleCredential(value string) bool {
	value = strings.TrimSpace(value)
	if scheme, credential, ok := strings.Cut(value, " "); ok && strings.EqualFold(scheme, "Bearer") {
		value = strings.TrimSpace(credential)
	}
	return strings.HasPrefix(value, "change-me-")
}
