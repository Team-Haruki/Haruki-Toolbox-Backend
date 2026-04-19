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
	KeyActionBinding     = "binding"

	KeyModuleConfig  = "config"
	KeyActionRuntime = "runtime"

	KeyModuleUpload     = "upload"
	KeyActionIOS        = "ios"
	KeyActionChunkMeta  = "chunk-meta"
	KeyActionChunkData  = "chunk-data"
	KeyActionChunkClaim = "chunk-claim"

	KeyModuleRateLimit     = "rate-limit"
	KeyActionUploadIngress = "upload-ingress"

	KeyModulePublicAPI = "public-api"
	KeyActionCache     = "cache"

	KeyModuleBot      = "bot"
	KeyActionRegister = "register"
)

func BuildBotVerifyCodeKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionVerify, hashNormalizedIdentifier(qq))
}

func BuildBotVerifyAttemptKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionVerify, KeyActionAttempt, hashNormalizedIdentifier(qq))
}

func BuildBotSendMailRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionSend, KeyDimensionIP, clientIP)
}

func BuildBotSendMailRateLimitTargetKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionSend, KeyDimensionUser, hashNormalizedIdentifier(qq))
}

func BuildBotRegisterRateLimitTargetKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionRegister, KeyActionAttempt, hashNormalizedIdentifier(qq))
}

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

func BuildSocialPlatformVerifyAttemptKey(platform, platformUserID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, KeyActionVerify, KeyActionAttempt, platform, platformUserID)
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

func BuildQQMailSendRateLimitUserKey(userID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, "qq-mail", KeyActionSend, "user", strings.TrimSpace(userID))
}

func BuildQQMailSendRateLimitTargetKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, "qq-mail", KeyActionSend, KeyDimensionUser, hashNormalizedIdentifier(qq))
}

func BuildStatusTokenKey(token string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, KeyActionStatusToken, token)
}

func BuildStatusTokenOwnerKey(token string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, KeyActionStatusToken, token, KeyActionUserID)
}

func BuildStatusTokenBindingKey(token string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, KeyActionStatusToken, token, KeyActionBinding)
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

func BuildUploadIngressRateLimitKey(windowUnix int64, bucket string) string {
	return buildKey(
		KeyPrefixHaruki,
		KeyModuleRateLimit,
		KeyActionUploadIngress,
		strconv.FormatInt(windowUnix, 10),
		bucket,
	)
}

func BuildIOSUploadChunkMetaKey(uploadKey string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleUpload, KeyActionIOS, KeyActionChunkMeta, uploadKey)
}

func BuildIOSUploadChunkDataKey(uploadKey string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleUpload, KeyActionIOS, KeyActionChunkData, uploadKey)
}

func BuildIOSUploadChunkClaimKey(uploadKey string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleUpload, KeyActionIOS, KeyActionChunkClaim, uploadKey)
}

func BuildRuntimeConfigKey() string {
	return buildKey(KeyPrefixHaruki, KeyModuleConfig, KeyActionRuntime)
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
