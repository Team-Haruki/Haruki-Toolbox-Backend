package oauth2

const (
	ScopeUserRead      = "user:read"
	ScopeBindingsRead  = "bindings:read"
	ScopeGameDataRead  = "game-data:read"
	ScopeGameDataWrite = "game-data:write"
)

var AllScopes = map[string]string{
	ScopeUserRead:      "Read your profile (name and avatar)",
	ScopeBindingsRead:  "Read your bound game accounts",
	ScopeGameDataRead:  "Read your uploaded game data",
	ScopeGameDataWrite: "Upload game data on your behalf",
}

func ValidateScopes(requested []string, clientAllowed []string) ([]string, bool) {
	allowedSet := make(map[string]struct{}, len(clientAllowed))
	for _, s := range clientAllowed {
		allowedSet[s] = struct{}{}
	}
	var validated []string
	for _, s := range requested {
		if _, ok := AllScopes[s]; !ok {
			return nil, false
		}
		if _, ok := allowedSet[s]; !ok {
			return nil, false
		}
		validated = append(validated, s)
	}
	if len(validated) == 0 {
		return nil, false
	}
	return validated, true
}

func HasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required {
			return true
		}
	}
	return false
}

func ScopeDescriptions(scopes []string) []string {
	var descs []string
	for _, s := range scopes {
		if desc, ok := AllScopes[s]; ok {
			descs = append(descs, desc)
		}
	}
	return descs
}
