package redis

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	harukiConfig "haruki-suite/config"
	"strconv"
	"strings"
)

const (
	KeyPrefixHaruki = "haruki"

	KeyModuleEmail   = "email"
	KeyActionVerify  = "verify"
	KeyActionResetPW = "reset-password"
	KeyActionLogin   = "login"
	KeyActionAttempt = "attempt"
	KeyActionSend    = "send"
	KeyDimensionIP   = "ip"
	KeyDimensionUser = "target"

	KeyModuleGameAccount = "game-account"

	KeyModuleSocial      = "social"
	KeyActionStatusToken = "status-token"
	KeyActionUserID      = "user-id"

	KeyModuleOAuth2 = "oauth2"
	KeyActionCode   = "code"

	KeyModuleRateLimit     = "rate-limit"
	KeyActionUploadIngress = "upload-ingress"

	KeyModulePublicAPI = "public-api"
	KeyActionCache     = "cache"
)

func BuildEmailVerifyKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, hashNormalizedIdentifier(email))
}

func BuildResetPasswordKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, hashNormalizedIdentifier(email))
}

func BuildGameAccountVerifyKey(userID, server, gameUserID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleGameAccount, KeyActionVerify, userID, server, gameUserID)
}

func BuildGameAccountVerifyAttemptKey(userID, server, gameUserID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleGameAccount, KeyActionVerify, KeyActionAttempt, userID, server, gameUserID)
}

func BuildSocialPlatformVerifyKey(platform, platformUserID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, platform, platformUserID)
}

func BuildSocialPlatformUserIDKey(platform, platformUserID string) string {
	return buildKey(
		KeyPrefixHaruki,
		KeyModuleSocial,
		KeyActionVerify,
		platform,
		platformUserID,
		KeyActionUserID,
	)
}

func BuildSocialPlatformStatusTokenKey(platform, platformUserID string) string {
	return buildKey(
		KeyPrefixHaruki,
		KeyModuleSocial,
		KeyActionVerify,
		platform,
		platformUserID,
		KeyActionStatusToken,
	)
}

func BuildStatusTokenKey(token string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, KeyActionStatusToken, token)
}

func BuildOTPAttemptKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionAttempt, hashNormalizedIdentifier(email))
}

func BuildEmailVerifySendRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, KeyActionSend, KeyDimensionIP, clientIP)
}

func BuildEmailVerifySendRateLimitTargetKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, KeyActionSend, KeyDimensionUser, hashNormalizedIdentifier(email))
}

func BuildResetPasswordSendRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionSend, KeyDimensionIP, clientIP)
}

func BuildResetPasswordSendRateLimitTargetKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionSend, KeyDimensionUser, hashNormalizedIdentifier(email))
}

func BuildResetPasswordApplyRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionAttempt, KeyDimensionIP, clientIP)
}

func BuildResetPasswordApplyRateLimitTargetKey(target string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionAttempt, KeyDimensionUser, hashNormalizedIdentifier(target))
}

func BuildLoginRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionLogin, KeyActionAttempt, KeyDimensionIP, clientIP)
}

func BuildLoginRateLimitTargetKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionLogin, KeyActionAttempt, KeyDimensionUser, hashNormalizedIdentifier(email))
}

func BuildOAuth2AuthCodeKey(code string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleOAuth2, KeyActionCode, code)
}

func BuildUploadIngressRateLimitKey(windowUnix int64, bucket string) string {
	return buildKey(
		KeyPrefixHaruki,
		KeyModuleRateLimit,
		KeyActionUploadIngress,
		strconv.FormatInt(windowUnix, 10),
		bucket,
	)
}

func buildKey(parts ...string) string {
	return strings.Join(parts, ":")
}

func hashNormalizedIdentifier(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if secret := strings.TrimSpace(harukiConfig.Cfg.UserSystem.SessionSignToken); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(normalized))
		return hex.EncodeToString(mac.Sum(nil))
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
