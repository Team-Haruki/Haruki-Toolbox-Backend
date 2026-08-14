package misc

import "fmt"

// AssetsConfigOptions contains the startup asset settings consumed by misc
// handlers.
type AssetsConfigOptions struct {
	AvatarBaseURL string
}

// AssetsConfig is an immutable view of the asset settings used by the misc
// module. The base URL is deliberately preserved verbatim because existing
// friend-link responses expose its historical slash-joining semantics.
type AssetsConfig struct {
	avatarBaseURL string
}

func NewAssetsConfig(options AssetsConfigOptions) AssetsConfig {
	return AssetsConfig{avatarBaseURL: options.AvatarBaseURL}
}

func (c AssetsConfig) FriendLinkURL(fileName string) string {
	return fmt.Sprintf("%s/friend-links/%s", c.avatarBaseURL, fileName)
}
