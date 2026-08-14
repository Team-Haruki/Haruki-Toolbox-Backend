package userprofile

import (
	"fmt"
	"strings"
)

// ConfigOptions contains the startup configuration consumed by user profile
// handlers. NewConfig copies and normalizes these values at the composition
// boundary so handlers do not depend on mutable process-wide configuration.
type ConfigOptions struct {
	AvatarSaveDir string
	AvatarBaseURL string
}

// Config is an immutable view of user profile configuration. Its fields remain
// private so all normalization happens once, before routes are registered.
type Config struct {
	avatarSaveDir string
	avatarBaseURL string
}

func NewConfig(options ConfigOptions) Config {
	return Config{
		avatarSaveDir: strings.TrimSpace(options.AvatarSaveDir),
		avatarBaseURL: strings.TrimRight(strings.TrimSpace(options.AvatarBaseURL), "/"),
	}
}

func (c Config) AvatarSaveDir() string {
	return c.avatarSaveDir
}

func (c Config) AvatarURL(fileName string) string {
	return fmt.Sprintf("%s/avatars/%s", c.avatarBaseURL, fileName)
}
