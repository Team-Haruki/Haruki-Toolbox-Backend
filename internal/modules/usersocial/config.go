package usersocial

import "crypto/subtle"

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
	return BotVerifyConfig{token: options.Token}
}

func (c BotVerifyConfig) authorizes(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) == 1
}
