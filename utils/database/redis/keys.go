package redis

import "fmt"

const (
	KeyPrefixHaruki = "haruki"

	KeyModuleEmail   = "email"
	KeyActionVerify  = "verify"
	KeyActionResetPW = "reset-password"

	KeyModuleGameAccount = "game-account"

	KeyModuleSocial      = "social"
	KeyActionStatusToken = "status-token"
	KeyActionUserID      = "user-id"

	KeyModulePublicAPI = "public-api"
	KeyActionCache     = "cache"
)

func BuildEmailVerifyKey(email string) string {
	return fmt.Sprintf("%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, email)
}

func BuildResetPasswordKey(email string) string {
	return fmt.Sprintf("%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, email)
}

func BuildGameAccountVerifyKey(userID, server, gameUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleGameAccount, KeyActionVerify, userID, server, gameUserID)
}

func BuildSocialPlatformVerifyKey(platform, platformUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, platform, platformUserID)
}

func BuildSocialPlatformUserIDKey(platform, platformUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, platform, platformUserID, KeyActionUserID)
}

func BuildSocialPlatformStatusTokenKey(platform, platformUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, platform, platformUserID, KeyActionStatusToken)
}

func BuildStatusTokenKey(token string) string {
	return fmt.Sprintf("%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionStatusToken, token)
}

func BuildOTPAttemptKey(email string) string {
	return fmt.Sprintf("%s:%s:attempt:%s", KeyPrefixHaruki, KeyModuleEmail, email)
}

func BuildOAuth2AuthCodeKey(code string) string {
	return fmt.Sprintf("%s:oauth2:code:%s", KeyPrefixHaruki, code)
}
