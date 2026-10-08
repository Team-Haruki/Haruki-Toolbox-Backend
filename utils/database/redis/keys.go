package redis

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

	KeyModuleRateLimit         = "rate-limit"
	KeyActionUploadIngress     = "upload-ingress"
	KeyActionBotSecurityIngest = "bot-security-ingest"

	KeyModulePublicAPI = "public-api"
	KeyActionCache     = "cache"

	KeyModuleBot      = "bot"
	KeyActionRegister = "register"

	// KeyModuleOAuth2Device holds RFC 8628 device flows; KeyActionOAuth2Device is
	// the device-flow namespace under KeyModuleRateLimit.
	KeyModuleOAuth2Device = "oauth2-device"
	KeyActionOAuth2Device = "oauth2-device"

	KeyModuleMysekaiBirthday = "mysekai-birthday"
	KeyActionMonitor         = "monitor"
	KeyActionSubscription    = "subscription"
	KeyActionEvent           = "event"
)

// KeyBuilder owns the startup secret used to pseudonymize sensitive
// identifiers in Redis keys. The zero value intentionally uses the historical
// SHA-256 fallback so tests and standalone utilities remain deterministic.
type KeyBuilder struct {
	identifierHashSecret string
}

func NewKeyBuilder(identifierHashSecret string) KeyBuilder {
	return KeyBuilder{identifierHashSecret: identifierHashSecret}
}

func (b KeyBuilder) BuildBotVerifyCodeKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionVerify, b.hashNormalizedIdentifier(qq))
}

func (b KeyBuilder) BuildBotVerifyAttemptKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionVerify, KeyActionAttempt, b.hashNormalizedIdentifier(qq))
}

func BuildBotSendMailRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionSend, KeyDimensionIP, clientIP)
}

func (b KeyBuilder) BuildBotSendMailRateLimitTargetKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionSend, KeyDimensionUser, b.hashNormalizedIdentifier(qq))
}

func (b KeyBuilder) BuildBotRegisterRateLimitTargetKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleBot, KeyActionRegister, KeyActionAttempt, b.hashNormalizedIdentifier(qq))
}

func (b KeyBuilder) BuildEmailVerifyKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, b.hashNormalizedIdentifier(email))
}

func (b KeyBuilder) BuildResetPasswordKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, b.hashNormalizedIdentifier(email))
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

func (b KeyBuilder) BuildQQMailSendRateLimitTargetKey(qq string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleSocial, "qq-mail", KeyActionSend, KeyDimensionUser, b.hashNormalizedIdentifier(qq))
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

func (b KeyBuilder) BuildOTPAttemptKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionAttempt, b.hashNormalizedIdentifier(email))
}

func BuildOTPVerifyAttemptIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, KeyActionAttempt, KeyDimensionIP, clientIP)
}

func BuildAfdianCallbackRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, "sponsor", "afdian", "callback", KeyDimensionIP, clientIP)
}

func BuildEmailVerifySendRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, KeyActionSend, KeyDimensionIP, clientIP)
}

func (b KeyBuilder) BuildEmailVerifySendRateLimitTargetKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionVerify, KeyActionSend, KeyDimensionUser, b.hashNormalizedIdentifier(email))
}

func BuildResetPasswordSendRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionSend, KeyDimensionIP, clientIP)
}

func (b KeyBuilder) BuildResetPasswordSendRateLimitTargetKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionSend, KeyDimensionUser, b.hashNormalizedIdentifier(email))
}

func BuildResetPasswordApplyRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionAttempt, KeyDimensionIP, clientIP)
}

func (b KeyBuilder) BuildResetPasswordApplyRateLimitTargetKey(target string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionResetPW, KeyActionAttempt, KeyDimensionUser, b.hashNormalizedIdentifier(target))
}

func BuildLoginRateLimitIPKey(clientIP string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionLogin, KeyActionAttempt, KeyDimensionIP, clientIP)
}

func (b KeyBuilder) BuildLoginRateLimitTargetKey(email string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleEmail, KeyActionLogin, KeyActionAttempt, KeyDimensionUser, b.hashNormalizedIdentifier(email))
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

// BuildBotSecurityIngestRateLimitKey counts bot security alerts accepted
// from one caller address in one fixed window.
func BuildBotSecurityIngestRateLimitKey(windowUnix int64, clientIP string) string {
	return buildKey(
		KeyPrefixHaruki,
		KeyModuleRateLimit,
		KeyActionBotSecurityIngest,
		strconv.FormatInt(windowUnix, 10),
		KeyDimensionIP,
		clientIP,
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

func BuildMysekaiBirthdayMonitorKey(server, gameUserID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleMysekaiBirthday, KeyActionMonitor, strings.TrimSpace(server), strings.TrimSpace(gameUserID))
}

func BuildMysekaiBirthdaySubscriptionKey(subscriptionID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleMysekaiBirthday, KeyActionSubscription, strings.TrimSpace(subscriptionID))
}

func BuildMysekaiBirthdayEventKey(subscriptionID, subscriptionVersion, eventID string) string {
	return buildKey(
		KeyPrefixHaruki,
		KeyModuleMysekaiBirthday,
		KeyActionEvent,
		strings.TrimSpace(subscriptionID),
		strings.TrimSpace(subscriptionVersion),
		strings.TrimSpace(eventID),
	)
}

func BuildMysekaiBirthdaySubscriptionEventsPattern(subscriptionID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleMysekaiBirthday, KeyActionEvent, strings.TrimSpace(subscriptionID), "*")
}

// Device-flow keys never carry a raw user code, wrapped device code or flow
// handle: each such value appears only as hashExactIdentifier(domain, value).
// The flow ID is a server-side random identifier and is used as is.

// BuildOAuth2DeviceFlowKey is the flow HASH haruki:oauth2-device:flow:{fid}.
func (b KeyBuilder) BuildOAuth2DeviceFlowKey(flowID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleOAuth2Device, "flow", flowID)
}

// BuildOAuth2DeviceCodeIndexKey maps a wrapped device code (hdc_…) to its flow.
func (b KeyBuilder) BuildOAuth2DeviceCodeIndexKey(wrappedDeviceCode string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleOAuth2Device, "dc", b.hashExactIdentifier("dc", wrappedDeviceCode))
}

// BuildOAuth2DeviceUserCodeIndexKey maps a normalized user code to its flow.
func (b KeyBuilder) BuildOAuth2DeviceUserCodeIndexKey(normalizedUserCode string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleOAuth2Device, "uc", b.hashExactIdentifier("uc", normalizedUserCode))
}

// BuildOAuth2DeviceFlowHandleIndexKey maps a browser flow handle (dfh_…) to its flow.
func (b KeyBuilder) BuildOAuth2DeviceFlowHandleIndexKey(flowHandle string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleOAuth2Device, "fh", b.hashExactIdentifier("fh", flowHandle))
}

// BuildOAuth2DeviceUnredeemedKey is the ZSET of flows that recorded a consent
// request ID but have not handed out tokens (member fid, score exp in ms).
func (b KeyBuilder) BuildOAuth2DeviceUnredeemedKey() string {
	return buildKey(KeyPrefixHaruki, KeyModuleOAuth2Device, "unredeemed")
}

// BuildOAuth2DeviceConsentRequestKey keeps the consent request ID of an
// unredeemed flow (STRING haruki:oauth2-device:crid:{fid}) beyond the flow
// HASH, so the reaper can still revoke the consent after the flow expired.
func (b KeyBuilder) BuildOAuth2DeviceConsentRequestKey(flowID string) string {
	return buildKey(KeyPrefixHaruki, KeyModuleOAuth2Device, "crid", flowID)
}

func (b KeyBuilder) BuildOAuth2DeviceAuthAttemptUnknownClientKey() string {
	return b.oauth2DeviceRateLimitKey("auth-attempt", "unknown-client")
}

func (b KeyBuilder) BuildOAuth2DeviceAuthAttemptClientKey(clientID string) string {
	return b.oauth2DeviceRateLimitKey("auth-attempt", "client", b.hashExactIdentifier("cid", clientID))
}

// BuildOAuth2DeviceAuthIssuedPoolKey is the global issuance pool of one client
// type ("public" or "confidential").
func (b KeyBuilder) BuildOAuth2DeviceAuthIssuedPoolKey(clientType string) string {
	return b.oauth2DeviceRateLimitKey("auth-issued", "global", clientType)
}

func (b KeyBuilder) BuildOAuth2DeviceAuthIssuedClientKey(clientID string) string {
	return b.oauth2DeviceRateLimitKey("auth-issued", "client", b.hashExactIdentifier("cid", clientID))
}

func (b KeyBuilder) BuildOAuth2DeviceLookupUserKey(userID string) string {
	return b.oauth2DeviceRateLimitKey("lookup", "user", b.hashExactIdentifier("uid", userID))
}

func (b KeyBuilder) BuildOAuth2DeviceLookupFailUserKey(userID string) string {
	return b.oauth2DeviceRateLimitKey("lookup-fail", "user", b.hashExactIdentifier("uid", userID))
}

func (b KeyBuilder) BuildOAuth2DeviceLookupFailUserDayKey(userID string) string {
	return b.oauth2DeviceRateLimitKey("lookup-fail", "user-day", b.hashExactIdentifier("uid", userID))
}

func (b KeyBuilder) BuildOAuth2DeviceLookupFailGlobalKey() string {
	return b.oauth2DeviceRateLimitKey("lookup-fail", "global")
}

func (b KeyBuilder) BuildOAuth2DeviceDecisionUserDayKey(userID string) string {
	return b.oauth2DeviceRateLimitKey("decision", "user-day", b.hashExactIdentifier("uid", userID))
}

// HashOAuth2DeviceIdentifier is hashExactIdentifier for values a device flow
// stores rather than keys on, such as the user-code hash kept in the flow.
func (b KeyBuilder) HashOAuth2DeviceIdentifier(domain, raw string) string {
	return b.hashExactIdentifier(domain, raw)
}

func (b KeyBuilder) oauth2DeviceRateLimitKey(parts ...string) string {
	return buildKey(append([]string{KeyPrefixHaruki, KeyModuleRateLimit, KeyActionOAuth2Device}, parts...)...)
}

func buildKey(parts ...string) string {
	return strings.Join(parts, ":")
}

func (b KeyBuilder) hashNormalizedIdentifier(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if secret := strings.TrimSpace(b.identifierHashSecret); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(normalized))
		return hex.EncodeToString(mac.Sum(nil))
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// hashExactIdentifier is hex(HMAC-SHA256(secret, domain+"\x00"+raw)). Unlike
// hashNormalizedIdentifier it neither trims nor lowercases: wrapped device codes
// are case-sensitive. The domain separates identifier kinds that could
// otherwise share a value. The secret-less SHA-256 fallback only serves tests
// and tools; startup refuses an enabled device flow without a secret.
func (b KeyBuilder) hashExactIdentifier(domain, raw string) string {
	message := domain + "\x00" + raw
	if secret := strings.TrimSpace(b.identifierHashSecret); secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(message))
		return hex.EncodeToString(mac.Sum(nil))
	}
	sum := sha256.Sum256([]byte(message))
	return hex.EncodeToString(sum[:])
}
