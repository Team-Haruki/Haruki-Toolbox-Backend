package usersponsor

import (
	"net/url"
	"strings"
)

// OAuthConfig is deliberately separate from Afdian's creator OpenAPI token.
// The callback is a fixed frontend route registered with Afdian, never a
// browser-supplied return URL.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

func (c OAuthConfig) Enabled() bool {
	u, err := url.Parse(c.RedirectURI)
	return strings.TrimSpace(c.ClientID) != "" && strings.TrimSpace(c.ClientSecret) != "" &&
		err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil &&
		u.Path == "/user/afdian/callback" && u.RawQuery == "" && u.Fragment == ""
}
