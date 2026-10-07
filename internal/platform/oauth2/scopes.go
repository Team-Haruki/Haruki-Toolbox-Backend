package oauth2

const (
	ScopeOpenID        = "openid"
	ScopeProfile       = "profile"
	ScopeEmail         = "email"
	ScopeOfflineAccess = "offline_access"
	ScopeUserRead      = "user:read"
	ScopeBindingsRead  = "bindings:read"
	ScopeGameDataRead  = "game-data:read"
	ScopeGameDataWrite = "game-data:write"
	// ScopeStationRoomWrite lets a client submit room numbers to Sekai Station
	// as the user. It is an ordinary scope any registered client may hold; its
	// risk class is write. Sekai Station checks it through the internal
	// introspection API, whatever the client.
	ScopeStationRoomWrite = "station:room:write"
)

var AllScopes = map[string]string{
	ScopeOpenID:        "Sign in with your Haruki Toolbox identity",
	ScopeProfile:       "Read your display name",
	ScopeEmail:         "Read your email address and verification status",
	ScopeOfflineAccess: "Request refresh_token issuance for long-lived delegated access",
	ScopeUserRead:      "Read your profile (name and avatar)",
	ScopeBindingsRead:  "Read your bound game accounts",
	ScopeGameDataRead:  "Read your uploaded game data",
	ScopeGameDataWrite: "Upload game data for accounts you own or have write permission for",
	// Scopes of other Haruki services.
	ScopeStationRoomWrite: "Submit room numbers to Sekai Station on your behalf",
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
