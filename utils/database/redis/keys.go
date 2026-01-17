package redis

import "fmt"

// Redis key prefixes - 统一格式: haruki:{module}:{action}:{identifier}
const (
	KeyPrefixHaruki = "haruki"

	// KeyModuleEmail Email related keys
	KeyModuleEmail   = "email"
	KeyActionVerify  = "verify"
	KeyActionResetPW = "reset-password"

	// KeyModuleGameAccount Game account related keys
	KeyModuleGameAccount = "game-account"

	// KeyModuleSocial Social platform related keys
	KeyModuleSocial      = "social"
	KeyActionStatusToken = "status-token"
	KeyActionUserID      = "user-id"

	// KeyModulePublicAPI Public API cache
	KeyModulePublicAPI = "public-api"
	KeyActionCache     = "cache"
)

// BuildEmailVerifyKey builds key for email verification code
// Format: haruki:email:verify:{email}
func BuildEmailVerifyKey(email string) string {
	return fmt.Sprintf("%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, email)
}

// BuildResetPasswordKey builds key for reset password token
// Format: haruki:email:reset-password:{email}
func BuildResetPasswordKey(email string) string {
	return fmt.Sprintf("%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, email)
}

// BuildGameAccountVerifyKey builds key for game account verification
// Format: haruki:game-account:verify:{userID}:{server}:{gameUserID}
func BuildGameAccountVerifyKey(userID, server, gameUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleGameAccount, KeyActionVerify, userID, server, gameUserID)
}

// BuildSocialPlatformVerifyKey builds key for social platform verification
// Format: haruki:social:verify:{platform}:{platformUserID}
func BuildSocialPlatformVerifyKey(platform, platformUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, platform, platformUserID)
}

// BuildSocialPlatformUserIDKey builds key for social platform user ID mapping
// Format: haruki:social:verify:{platform}:{platformUserID}:user-id
func BuildSocialPlatformUserIDKey(platform, platformUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, platform, platformUserID, KeyActionUserID)
}

// BuildSocialPlatformStatusTokenKey builds key for social platform status token mapping
// Format: haruki:social:verify:{platform}:{platformUserID}:status-token
func BuildSocialPlatformStatusTokenKey(platform, platformUserID string) string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, platform, platformUserID, KeyActionStatusToken)
}

// BuildStatusTokenKey builds key for status token
// Format: haruki:social:status-token:{token}
func BuildStatusTokenKey(token string) string {
	return fmt.Sprintf("%s:%s:%s:%s", KeyPrefixHaruki, KeyModuleSocial, KeyActionStatusToken, token)
}

// BuildOTPAttemptKey builds key for tracking OTP verification attempts
// Format: haruki:email:attempt:{email}
func BuildOTPAttemptKey(email string) string {
	return fmt.Sprintf("%s:%s:attempt:%s", KeyPrefixHaruki, KeyModuleEmail, email)
}
