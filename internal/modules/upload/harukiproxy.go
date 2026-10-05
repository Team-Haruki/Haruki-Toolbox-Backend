package upload

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/hashicorp/go-version"
)

func unpackKeyFromHelper(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) ([]byte, error) {
	_, _, _, unpackKey := apiHelper.GetHarukiProxyConfig()
	return deriveHarukiProxyKey(unpackKey)
}

func deriveHarukiProxyKey(unpackKey string) ([]byte, error) {
	k := strings.TrimSpace(unpackKey)
	if k == "" {
		return nil, errors.New("missing HarukiProxyUnpackKey")
	}
	sum := sha256.Sum256([]byte(k))
	return sum[:], nil
}

var userAgentRegex = regexp.MustCompile(`^([A-Za-z0-9\-]+)/([vV][0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9]+)?)$`)

func validateHarukiProxyClientHeader(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return validateHarukiProxyClientHeaderVersion(apiHelper, Dependencies{}, false)
}

func validateHarukiProxyClientHeaderVersion(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, v3 bool) fiber.Handler {
	if v3 {
		return validateProxyV3Client(apiHelper, dependencies)
	}
	return func(c fiber.Ctx) error {
		expectedUserAgent, minVersion, expectedSecret, _ := apiHelper.GetHarukiProxyConfig()
		if strings.TrimSpace(expectedUserAgent) == "" || strings.TrimSpace(minVersion) == "" || strings.TrimSpace(expectedSecret) == "" {
			return proxyResponse(c, 500, "internal_error", "HarukiProxy auth is not configured", false, nil)
		}

		requestUserAgent := c.Get("User-Agent")
		requestSecret := c.Get("X-Haruki-Toolbox-Secret")
		if subtle.ConstantTimeCompare([]byte(requestSecret), []byte(expectedSecret)) != 1 {
			return proxyResponse(c, 400, "invalid_client_credentials", "Invalid HarukiProxy Secret", false, nil)
		}
		matches := userAgentRegex.FindStringSubmatch(requestUserAgent)
		if len(matches) < 3 {
			return proxyResponse(c, 400, "invalid_client_metadata", "Invalid User-Agent format", false, nil)
		}
		uaName := matches[1]
		if expectedUserAgent != uaName {
			return proxyResponse(c, 400, "invalid_client_metadata", "Invalid User-Agent name", false, nil)
		}
		clientVerStr := strings.TrimPrefix(matches[2], "v")
		minVerStr := strings.TrimPrefix(minVersion, "v")
		clientVer, err1 := version.NewVersion(clientVerStr)
		minVer, err2 := version.NewVersion(minVerStr)
		if err1 != nil || err2 != nil {
			return proxyResponse(c, 400, "invalid_client_metadata", "Invalid version string", false, nil)
		}
		if clientVer.LessThan(minVer) {
			return proxyResponse(c, 400, "client_version_unsupported", fmt.Sprintf("Client version %s is below minimum required %s", clientVerStr, minVersion), false, nil)
		}
		return c.Next()
	}
}

func Unpack(body []byte, aad string, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) ([]byte, error) {
	key, err := unpackKeyFromHelper(apiHelper)
	if err != nil {
		return nil, err
	}
	return unpackHarukiProxyBody(body, aad, key)
}

func unpackHarukiProxyBody(body []byte, aad string, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(body) < nonceSize+gcm.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	nonce := body[:nonceSize]
	ciphertext := body[nonceSize:]
	var aadBytes []byte
	if len(aad) > 0 {
		aadBytes = []byte(aad)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aadBytes)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func handleHarukiProxyUpload(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) fiber.Handler {
	return handleHarukiProxyUploadVersion(apiHelper, dependencies, false)
}

func handleHarukiProxyUploadVersion(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, v3 bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		a := proxyAttempt(c)
		invalid := func(message string) error {
			return proxyResponse(c, 400, "invalid_upload_payload", message, false, nil)
		}
		serverStr, gameUserIDStr, dataTypeStr := c.Params("server"), c.Params("user_id"), c.Params("data_type")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return invalid("invalid server")
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return invalid("invalid data_type")
		}
		gameUserID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil || gameUserID <= 0 {
			return invalid("invalid user_id")
		}
		c.Locals(proxyIngressKey{}, "accepted")
		if !v3 {
			a.Client.Protocol = "2"
			a.Client.Name = "HarukiProxy"
			a.Client.Format = "legacy"
			if matches := userAgentRegex.FindStringSubmatch(c.Get("User-Agent")); len(matches) == 3 {
				a.Client.Version = strings.Clone(strings.TrimPrefix(matches[2], "v"))
			}
		}
		rawBody := c.Request().Body()
		aad := fmt.Sprintf("%s|%s|%s", serverStr, gameUserIDStr, dataTypeStr)
		var decryptedBody []byte
		var dErr error
		var actor *string
		requestDependencies := dependencies
		if v3 {
			id, ok := c.Locals("userID").(string)
			if !ok || id == "" {
				return proxyResponse(c, 401, "invalid_token", "OAuth2 authentication required", false, nil)
			}
			actor = &id
			requestDependencies = oauthUploadDependencies(c, apiHelper, dependencies)
			decryptedBody = rawBody
		} else {
			decryptedBody, dErr = Unpack(rawBody, aad, apiHelper)
		}
		if dErr != nil {
			a.FailureStage = "decrypt"
			a.ErrorCode = "payload_decryption_failed"
			a.HTTPStatus = 400
			uc, _ := buildUploadContext(server, dataType, &gameUserID, nil, harukiUtils.UploadMethodHarukiProxy)
			uc.Attempt = a
			uc.FailureStage = "decrypt"
			finishAttempt(uc, false)
			message := "failed to decrypt request body"
			dispatchUploadAuditLog(apiHelper, dependencies.DataHandlerLogger, dependencies.BackgroundTasks, uc, false, &message)
			return proxyResponse(c, 400, a.ErrorCode, message, false, nil)
		}
		_, err = HandleUpload(c.Context(), decryptedBody, server, dataType, &gameUserID, actor, apiHelper, requestDependencies, harukiUtils.UploadMethodHarukiProxy, a)
		if err != nil {
			if a.ErrorCode == "" {
				classifyAttemptFailure(a, uploadStageBuildContext, err)
			}
			message := "failed to process upload"
			if a.HTTPStatus == 403 {
				message = "upload not allowed"
			}
			return proxyResponse(c, a.HTTPStatus, a.ErrorCode, message, a.Retryable, nil)
		}
		return proxyResponse(c, 200, "", "Upload successful", false, nil)
	}
}

// The cutoff is an absolute UTC+8 instant, independent of the server timezone.
var harukiProxyLegacySunset = time.Date(2026, time.November, 1, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))

func harukiProxyLegacyGate(now func() time.Time) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("Sunset", harukiProxyLegacySunset.UTC().Format(http.TimeFormat))
		if !now().Before(harukiProxyLegacySunset) {
			return proxyResponse(c, fiber.StatusGone, "protocol_retired", "Legacy HarukiProxy upload has been retired; use /harukiproxy/v3", false, nil)
		}
		return c.Next()
	}
}

func registerHarukiProxyRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	for _, prefix := range []string{"/harukiproxy", "/api/harukiproxy"} {
		apiHelper.Router.Post(prefix+"/v3/:server/:user_id/:data_type/upload",
			proxyIngress(apiHelper, "3"), proxyOAuthAuthentication(apiHelper, dependencies), validateHarukiProxyClientHeaderVersion(apiHelper, dependencies, true), handleHarukiProxyUploadVersion(apiHelper, dependencies, true))
		apiHelper.Router.Post(prefix+"/:server/:user_id/:data_type/upload",
			proxyIngress(apiHelper, "2"), harukiProxyLegacyGate(time.Now), validateHarukiProxyClientHeader(apiHelper), handleHarukiProxyUpload(apiHelper, dependencies))
	}
}
