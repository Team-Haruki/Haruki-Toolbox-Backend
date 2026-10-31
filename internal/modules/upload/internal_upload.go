package upload

// The internal upload endpoint lets a trusted service of our own write game
// data through the same pipeline as every other upload, without the client
// envelope that public upload clients use.
//
// Contract:
//
//	POST /internal/upload/:server/:user_id/:data_type
//	Authorization: Bearer <service token>
//	Content-Type: application/octet-stream
//	body: the game payload exactly as the game server encrypts it
//
// server is jp, en, tw, kr or cn; data_type is suite or mysekai; user_id is
// the game user ID the payload belongs to. The answer has the same JSON shape
// as the client upload routes (status, message, updatedData.error_code,
// updatedData.request_id, updatedData.retryable) and an X-Request-ID header.
//
// Exposure: the route lives on the backend port only. Oathkeeper has no rule
// for /internal/ (architecture test TestInternalAPINotRoutedByOathkeeper), so
// it is reachable only on the private network, never via the public proxy.
// It is registered only when internal_upload.token_sha256_file names a file
// holding the hex SHA-256 of the service token; the token itself never sits
// in backend config.
//
// Storage semantics are those of HandleUpload with no acting Toolbox user,
// the same as the other trusted anonymous paths: the payload identity must
// match user_id, a banned owner or a disabled CN MySekai upload is refused
// with 403, and the write, upload_time, cache invalidation, upload log and
// fan-out are the shared ones.

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platformAuthHeader "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/authheader"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// InternalUploadPath is the route pattern of the internal upload endpoint.
const InternalUploadPath = "/internal/upload/:server/:user_id/:data_type"

const (
	internalUploadAuthMethod = "service_token"

	// internalUploadMaxBodyBytes bounds one payload. A full suite is a few
	// megabytes; this matches the limit of the other upload paths.
	internalUploadMaxBodyBytes = harukiUtils.MaxBodySize

	internalUploadRateLimitWindow  = time.Minute
	internalUploadRateLimitPerIP   = 600
	internalUploadRateLimitKeyTTL  = 2 * internalUploadRateLimitWindow
	internalUploadRateLimitTimeout = time.Second

	// internalUploadTokenFileMaxBytes bounds what is read of the token file:
	// a hex digest and a trailing newline.
	internalUploadTokenFileMaxBytes = 1 << 10
)

// ErrInternalUploadToken is wrapped by every token file error. No error
// message carries the file's content.
var ErrInternalUploadToken = errors.New("internal upload token")

// InternalUploadConfig is the immutable configuration of the internal upload
// endpoint. Its zero value is disabled: the route is not registered.
type InternalUploadConfig struct {
	tokenSHA256 []byte
}

// LoadInternalUploadConfig reads the hex SHA-256 (64 characters, either case,
// surrounding whitespace ignored) of the service token from path.
func LoadInternalUploadConfig(path string) (InternalUploadConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return InternalUploadConfig{}, fmt.Errorf("%w: no token file configured", ErrInternalUploadToken)
	}
	// A FIFO or device would block the open or the read and hang startup.
	info, err := os.Stat(path)
	if err != nil {
		return InternalUploadConfig{}, fmt.Errorf("%w: cannot read token file", ErrInternalUploadToken)
	}
	if !info.Mode().IsRegular() {
		return InternalUploadConfig{}, fmt.Errorf("%w: token file must be a regular file", ErrInternalUploadToken)
	}
	file, err := os.Open(path)
	if err != nil {
		return InternalUploadConfig{}, fmt.Errorf("%w: cannot read token file", ErrInternalUploadToken)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, internalUploadTokenFileMaxBytes+1))
	if err != nil {
		return InternalUploadConfig{}, fmt.Errorf("%w: cannot read token file", ErrInternalUploadToken)
	}
	if len(raw) > internalUploadTokenFileMaxBytes {
		return InternalUploadConfig{}, fmt.Errorf("%w: token file must hold only a hex SHA-256 digest", ErrInternalUploadToken)
	}
	return ParseInternalUploadTokenSHA256(string(raw))
}

// ParseInternalUploadTokenSHA256 validates the hex SHA-256 of the service
// token. Errors never repeat the value.
func ParseInternalUploadTokenSHA256(value string) (InternalUploadConfig, error) {
	value = strings.TrimSpace(value)
	if len(value) != 2*sha256.Size {
		return InternalUploadConfig{}, fmt.Errorf("%w: token file must hold %d hex characters, got %d", ErrInternalUploadToken, 2*sha256.Size, len(value))
	}
	sum, err := hex.DecodeString(value)
	if err != nil {
		return InternalUploadConfig{}, fmt.Errorf("%w: token file must hold hex", ErrInternalUploadToken)
	}
	return InternalUploadConfig{tokenSHA256: sum}, nil
}

// Enabled reports whether the endpoint is configured.
func (c InternalUploadConfig) Enabled() bool {
	return len(c.tokenSHA256) == sha256.Size
}

// authorize checks "Authorization: Bearer <token>" against the configured
// hash in constant time. Exactly one Authorization header is accepted. It
// returns "" on success, otherwise the logged reason: "missing", "multiple",
// "malformed" or "mismatch". No reason carries a credential.
func (c InternalUploadConfig) authorize(headers [][]byte) string {
	switch len(headers) {
	case 0:
		return "missing"
	case 1:
	default:
		return "multiple"
	}
	header := string(headers[0])
	if strings.TrimSpace(header) == "" {
		return "missing"
	}
	token, ok := platformAuthHeader.ExtractBearerToken(header)
	if !ok {
		return "malformed"
	}
	sum := sha256.Sum256([]byte(token))
	if !c.Enabled() || subtle.ConstantTimeCompare(sum[:], c.tokenSHA256) != 1 {
		return "mismatch"
	}
	return ""
}

// parseInternalUploadDataType accepts the data types a whole-payload upload
// can carry. The birthday party type needs a party ID and its own route.
func parseInternalUploadDataType(value string) (harukiUtils.UploadDataType, bool) {
	switch dataType := harukiUtils.UploadDataType(value); dataType {
	case harukiUtils.UploadDataTypeSuite, harukiUtils.UploadDataTypeMysekai:
		return dataType, true
	default:
		return "", false
	}
}

// internalUploadAttempt creates the request's attempt record from the raw
// body length. c.Body() is not used: it would decode a Content-Encoding
// before the caller is authenticated.
func internalUploadAttempt(c fiber.Ctx) *platform.Attempt {
	if a, ok := c.Locals(proxyAttemptKey{}).(*platform.Attempt); ok {
		return a
	}
	a := &platform.Attempt{
		RequestID:    uuid.NewString(),
		ReceivedAt:   time.Now().UTC(),
		RequestBytes: int64(len(c.Request().Body())),
		AuthMethod:   internalUploadAuthMethod,
	}
	a.Client.Platform = "not_applicable"
	c.Locals(proxyAttemptKey{}, a)
	c.Set("X-Request-ID", a.RequestID)
	return a
}

// identityContentEncoding reports whether every Content-Encoding field (and
// every coding listed in one) is absent or identity.
func identityContentEncoding(fields [][]byte) bool {
	for _, field := range fields {
		for coding := range strings.SplitSeq(string(field), ",") {
			if coding = strings.TrimSpace(coding); coding != "" && !strings.EqualFold(coding, "identity") {
				return false
			}
		}
	}
	return true
}

// registerInternalUploadRoutes registers the internal upload endpoint when
// it is configured; otherwise nothing is registered.
func registerInternalUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	if apiHelper == nil || apiHelper.Router == nil || !dependencies.InternalUpload.Enabled() {
		return
	}
	logger := harukiLogger.NewLoggerFromGlobal("InternalUpload")
	apiHelper.Router.Post(InternalUploadPath, handleInternalUpload(apiHelper, dependencies, time.Now, logger))
}

func handleInternalUpload(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, now func() time.Time, logger *harukiLogger.Logger) fiber.Handler {
	cfg := dependencies.InternalUpload
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		a := internalUploadAttempt(c)

		if reason := cfg.authorize(c.Request().Header.PeekAll(fiber.HeaderAuthorization)); reason != "" {
			logger.Warnf("internal_upload event=unauthorized reason=%s ip=%s request_id=%s", reason, strconv.Quote(c.IP()), a.RequestID)
			return proxyResponse(c, fiber.StatusUnauthorized, "invalid_client_credentials", "unauthorized", false, nil)
		}
		if retryAfter, limited := internalUploadRateLimited(c, apiHelper, now().UTC(), logger); limited {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retryAfter))
			logger.Warnf("internal_upload event=rate_limited ip=%s request_id=%s", strconv.Quote(c.IP()), a.RequestID)
			return proxyResponse(c, fiber.StatusTooManyRequests, "rate_limited", "too many requests", true, nil)
		}

		invalid := func(status int, message string) error {
			return proxyResponse(c, status, "invalid_upload_payload", message, false, nil)
		}
		if !identityContentEncoding(c.Request().Header.PeekAll(fiber.HeaderContentEncoding)) {
			return invalid(fiber.StatusUnsupportedMediaType, "content encoding is not supported")
		}
		body := c.Request().Body()
		if len(body) > internalUploadMaxBodyBytes {
			return invalid(fiber.StatusRequestEntityTooLarge, "payload too large")
		}
		if len(body) == 0 {
			return invalid(fiber.StatusBadRequest, "empty payload")
		}
		server, err := harukiUtils.ParseSupportedDataUploadServer(c.Params("server"))
		if err != nil {
			return invalid(fiber.StatusBadRequest, "invalid server")
		}
		dataType, ok := parseInternalUploadDataType(c.Params("data_type"))
		if !ok {
			return invalid(fiber.StatusBadRequest, "invalid data_type")
		}
		gameUserID, err := strconv.ParseInt(c.Params("user_id"), 10, 64)
		if err != nil || gameUserID <= 0 {
			return invalid(fiber.StatusBadRequest, "invalid user_id")
		}

		_, err = HandleUpload(c.Context(), body, server, dataType, &gameUserID, nil, apiHelper, dependencies, harukiUtils.UploadMethodInternal, a)
		if err != nil {
			if a.ErrorCode == "" {
				classifyAttemptFailure(a, uploadStageBuildContext, err)
			}
			logger.Warnf("internal_upload event=rejected server=%s data_type=%s user_id=%d status=%d error_code=%s stage=%s request_id=%s",
				server, dataType, gameUserID, a.HTTPStatus, a.ErrorCode, a.FailureStage, a.RequestID)
			message := "failed to process upload"
			if a.HTTPStatus == fiber.StatusForbidden {
				message = "upload not allowed"
			}
			return proxyResponse(c, a.HTTPStatus, a.ErrorCode, message, a.Retryable, nil)
		}
		logger.Infof("internal_upload event=accepted server=%s data_type=%s user_id=%d bytes=%d request_id=%s",
			server, dataType, gameUserID, len(body), a.RequestID)
		return proxyResponse(c, fiber.StatusOK, "", "Upload successful", false, nil)
	}
}

// internalUploadRateLimited counts the caller's uploads in a fixed one-minute
// window in Redis. It fails open (and logs) when Redis is missing or
// erroring: the caller is authenticated, and losing its uploads is worse than
// an unbounded window. The upload pipeline's own concurrency limit still
// applies.
func internalUploadRateLimited(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, now time.Time, logger *harukiLogger.Logger) (int, bool) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
		return 0, false
	}
	window := int64(internalUploadRateLimitWindow / time.Second)
	slot := now.Unix() / window
	ctx, cancel := context.WithTimeout(c.Context(), internalUploadRateLimitTimeout)
	defer cancel()
	count, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, harukiRedis.BuildInternalUploadRateLimitKey(slot, c.IP()), internalUploadRateLimitKeyTTL)
	if err != nil {
		logger.Warnf("internal_upload event=rate_limit_unavailable error_type=%T", err)
		return 0, false
	}
	if count <= internalUploadRateLimitPerIP {
		return 0, false
	}
	return max(1, int(window-now.Unix()%window)), true
}
