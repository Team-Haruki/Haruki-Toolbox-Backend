package identity

import "strings"

// NormalizeEmail trims surrounding spaces and lower-cases the address.
func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
