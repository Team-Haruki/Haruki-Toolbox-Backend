package upload

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"regexp"
	"strconv"
	"strings"

	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/hashicorp/go-version"
)

func unpackKeyFromHelper(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) ([]byte, error) {
	k := strings.TrimSpace(apiHelper.HarukiProxyUnpackKey)
	if k == "" {
		return nil, errors.New("missing HarukiProxyUnpackKey")
	}
	sum := sha256.Sum256([]byte(k))
	return sum[:], nil
}

func validateHarukiProxyClientHeader(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	if apiHelper.HarukiProxyUserAgent != "" && apiHelper.HarukiProxyVersion != "" && apiHelper.HarukiProxySecret != "" {
		return func(c *fiber.Ctx) error {
			requestUserAgent := c.Get("User-Agent")
			requestSecret := c.Get("X-Haruki-Toolbox-Secret")

			if requestSecret != apiHelper.HarukiProxySecret {
				fmt.Println(apiHelper.HarukiProxySecret)
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid HarukiProxy Secret", nil)
			}

			re := regexp.MustCompile(`^([A-Za-z0-9\-]+)/([vV][0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9]+)?)$`)
			matches := re.FindStringSubmatch(requestUserAgent)
			if len(matches) < 3 {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid User-Agent format", nil)
			}

			uaName := matches[1]
			if apiHelper.HarukiProxyUserAgent != uaName {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid User-Agent name", nil)
			}

			clientVerStr := matches[2]
			clientVerStr = strings.TrimPrefix(clientVerStr, "v")
			minVerStr := strings.TrimPrefix(apiHelper.HarukiProxyVersion, "v")
			clientVer, err1 := version.NewVersion(clientVerStr)
			minVer, err2 := version.NewVersion(minVerStr)
			if err1 != nil || err2 != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid version string", nil)
			}
			if clientVer.LessThan(minVer) {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, fmt.Sprintf("Client version %s is below minimum required %s", clientVerStr, apiHelper.HarukiProxyVersion), nil)
			}

			return c.Next()
		}
	}

	return func(c *fiber.Ctx) error {
		return c.Next()
	}
}

func Unpack(body []byte, aad string, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) ([]byte, error) {
	key, err := unpackKeyFromHelper(apiHelper)
	if err != nil {
		return nil, err
	}
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

func registerHarukiProxyRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/harukiproxy/:server/:user_id/:data_type", validateHarukiProxyClientHeader(apiHelper))

	api.Post("/upload", func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("user_id")
		dataTypeStr := c.Params("data_type")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		gameUserIDInt, err := strconv.Atoi(gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid user_id", nil)
		}
		gameUserID := int64(gameUserIDInt)
		rawBody := c.Request().Body()
		aad := fmt.Sprintf("%s|%s|%s", serverStr, gameUserIDStr, dataTypeStr)
		decryptedBody, dErr := Unpack(rawBody, aad, apiHelper)
		if dErr != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, fmt.Sprintf("failed to decrypt request body: %v", dErr), nil)
		}

		_, err = HandleUpload(
			context.Background(),
			decryptedBody,
			server,
			dataType,
			&gameUserID,
			nil,
			apiHelper,
		)

		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, fmt.Sprintf("%s server user %d successfully uploaded suite data.", serverStr, gameUserID), nil)
	})
}
