package usersocial

import (
	"crypto/subtle"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
)

// BotVerifyConfigOptions contains the startup secret used by the social
// platform bot verification endpoint.
type BotVerifyConfigOptions struct {
	Token string
}

// BotVerifyConfig is an immutable, per-application view of the bot
// verification secret. Keeping the field private prevents handlers from
// reaching back into mutable process-wide configuration.
type BotVerifyConfig struct {
	token string
}

func NewBotVerifyConfig(options BotVerifyConfigOptions) BotVerifyConfig {
	if config.IsExampleCredential(options.Token) {
		return BotVerifyConfig{}
	}
	return BotVerifyConfig{token: options.Token}
}

func (c BotVerifyConfig) authorizes(token string) bool {
	return strings.TrimSpace(c.token) != "" && subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) == 1
}
