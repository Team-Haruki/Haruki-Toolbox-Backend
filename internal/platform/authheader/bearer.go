package authheader

import "strings"

// ExtractBearerToken parses an Authorization header and returns the bearer token.
// The scheme name is case-insensitive per RFC 7235.
func ExtractBearerToken(authHeader string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(authHeader))
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}
