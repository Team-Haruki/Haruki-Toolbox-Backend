package upload

import (
	"strconv"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func applyProxyResponseHeaders(c fiber.Ctx, headers map[string][]string) {
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		c.Set(key, values[0])
		if len(values) > 1 {
			for _, value := range values[1:] {
				c.Append(key, value)
			}
		}
	}
}

// Seams for the iOS upload handler tests: the game server call and the upload
// pipeline (whose success needs the game data store).
var (
	proxyCallAPI = sekai.HarukiSekaiProxyCallAPI
	runUpload    = HandleUpload
)

func HandleProxyUpload(
	proxy string,
	dataType harukiUtils.UploadDataType,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	dependencies Dependencies,
	mysekaiBirthdayPartyID *int64,
) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		serverStr := c.Params("server")
		userIDStr := c.Params("user_id")
		if userIDStr == "" {
			return fiber.NewError(fiber.StatusBadRequest, "invalid user_id")
		}
		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid user_id format")
		}
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid server")
		}
		if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty &&
			(mysekaiBirthdayPartyID == nil || *mysekaiBirthdayPartyID == 0) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid birthday party_id")
		}
		headers := make(map[string]string)
		for k, v := range c.Request().Header.All() {
			headers[string(append([]byte(nil), k...))] = string(append([]byte(nil), v...))
		}
		var body []byte
		if c.Method() == fiber.MethodPost || c.Method() == fiber.MethodPut || c.Method() == fiber.MethodPatch {
			body = c.Body()
		}
		params := c.Queries()
		resp, err := proxyCallAPI(
			ctx,
			headers,
			c.Method(),
			server,
			dataType,
			body,
			params,
			proxy,
			userID,
			mysekaiBirthdayPartyID,
		)
		if err != nil {
			harukiLogger.Warnf("Proxy upload upstream call failed for %s/%s: %v", serverStr, dataType, err)
			setProxyUploadResult(c, proxyUploadFailed, "game_server_unavailable")
			return fiber.NewError(fiber.StatusInternalServerError, "proxy upstream request failed")
		}
		if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty {
			unpackedData, err := dependencies.ServerCryptor.Unpack(resp.RawBody, server)
			if err != nil {
				harukiLogger.Warnf("Proxy upload unpack failed for %s/%s/%s: %v", serverStr, userIDStr, dataType, err)
				setProxyUploadResult(c, proxyUploadFailed, "invalid_upload_payload")
				return fiber.NewError(fiber.StatusInternalServerError, "failed to parse proxy response")
			}
			dataMap, ok := unpackedData.(map[string]any)
			if !ok {
				setProxyUploadResult(c, proxyUploadFailed, "invalid_upload_payload")
				return fiber.NewError(fiber.StatusInternalServerError, "invalid response data format")
			}
			isRefreshed, ok := dataMap["isRefreshed"].(bool)
			if !ok || !isRefreshed {
				applyProxyResponseHeaders(c, resp.NewHeaders)
				setProxyUploadResult(c, proxyUploadSkipped, "")
				return c.Status(resp.StatusCode).Send(resp.RawBody)
			}
		}
		attempt := newAttempt(harukiUtils.UploadMethodIOSProxy, len(resp.RawBody))
		if _, err := runUpload(ctx, resp.RawBody, server, dataType, &userID, nil, helper, dependencies, harukiUtils.UploadMethodIOSProxy, attempt); err != nil {
			_, code := uploadFailure(attempt)
			setProxyUploadResult(c, proxyUploadFailed, code)
			if mapped := mapUploadProcessingError(err); mapped != nil {
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
			}
			harukiLogger.Warnf("Proxy upload persist failed for %s/%s/%s: %v", serverStr, userIDStr, dataType, err)
			return fiber.NewError(fiber.StatusInternalServerError, "failed to process uploaded data")
		}
		applyProxyResponseHeaders(c, resp.NewHeaders)
		setProxyUploadResult(c, proxyUploadOK, "")
		return c.Status(resp.StatusCode).Send(resp.RawBody)
	}
}

// Proxy-mode responses say how the upload went, for a module script that
// notifies the user: X-Haruki-Upload-Result is ok, failed or skipped (the
// response carried nothing to upload), and on failure X-Haruki-Upload-Error
// holds the public error code (invalid_upload_payload, upload_not_allowed,
// temporarily_unavailable, internal_error, game_server_unavailable).
const (
	proxyUploadResultHeader = "X-Haruki-Upload-Result"
	proxyUploadErrorHeader  = "X-Haruki-Upload-Error"

	proxyUploadOK      = "ok"
	proxyUploadFailed  = "failed"
	proxyUploadSkipped = "skipped"
)

func setProxyUploadResult(c fiber.Ctx, result, code string) {
	c.Set(proxyUploadResultHeader, result)
	if code != "" {
		c.Set(proxyUploadErrorHeader, code)
	}
}
