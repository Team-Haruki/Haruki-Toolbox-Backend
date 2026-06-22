package oauth2

const (
	ScopeOfflineAccess = "offline_access"
	ScopeUserRead      = "user:read"
	ScopeBindingsRead  = "bindings:read"
	ScopeGameDataRead  = "game-data:read"
	ScopeGameDataWrite = "game-data:write"
)

var AllScopes = map[string]string{
	ScopeOfflineAccess: "Request refresh_token issuance for long-lived delegated access",
	ScopeUserRead:      "Read your profile (name and avatar)",
	ScopeBindingsRead:  "Read your bound game accounts",
	ScopeGameDataRead:  "Read your uploaded game data",
	ScopeGameDataWrite: "Upload game data on your behalf",
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
