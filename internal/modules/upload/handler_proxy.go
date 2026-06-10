package upload

import (
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
	"strconv"

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

func HandleProxyUpload(
	proxy string,
	dataType harukiUtils.UploadDataType,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
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
		resp, err := sekai.HarukiSekaiProxyCallAPI(
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
			return fiber.NewError(fiber.StatusInternalServerError, "proxy upstream request failed")
		}
		if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty {
			unpackedData, err := sekai.Unpack(resp.RawBody, server)
			if err != nil {
				harukiLogger.Warnf("Proxy upload unpack failed for %s/%s/%s: %v", serverStr, userIDStr, dataType, err)
				return fiber.NewError(fiber.StatusInternalServerError, "failed to parse proxy response")
			}
			dataMap, ok := unpackedData.(map[string]any)
			if !ok {
				return fiber.NewError(fiber.StatusInternalServerError, "invalid response data format")
			}
			isRefreshed, ok := dataMap["isRefreshed"].(bool)
			if !ok || !isRefreshed {
				applyProxyResponseHeaders(c, resp.NewHeaders)
				return c.Status(resp.StatusCode).Send(resp.RawBody)
			}
		}
		if _, err := HandleUpload(ctx, resp.RawBody, server, dataType, &userID, nil, helper, harukiUtils.UploadMethodIOSProxy); err != nil {
			if mapped := mapUploadProcessingError(err); mapped != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
			}
			harukiLogger.Warnf("Proxy upload persist failed for %s/%s/%s: %v", serverStr, userIDStr, dataType, err)
			return fiber.NewError(fiber.StatusInternalServerError, "failed to process uploaded data")
		}
		applyProxyResponseHeaders(c, resp.NewHeaders)
		return c.Status(resp.StatusCode).Send(resp.RawBody)
	}
}
