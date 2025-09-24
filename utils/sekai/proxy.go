package sekai

import (
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiHttp "haruki-suite/utils/http"
	harukiMongo "haruki-suite/utils/mongo"
	harukiRedis "haruki-suite/utils/redis"
	urlParse "net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// 允许透传的请求头
var allowedHeaders = map[string]struct{}{
	"user-agent":        {},
	"cookie":            {},
	"x-forwarded-for":   {},
	"accept-language":   {},
	"accept":            {},
	"accept-encoding":   {},
	"x-devicemodel":     {},
	"x-app-hash":        {},
	"x-operatingsystem": {},
	"x-kc":              {},
	"x-unity-version":   {},
	"x-app-version":     {},
	"x-platform":        {},
	"x-session-token":   {},
	"x-asset-version":   {},
	"x-request-id":      {},
	"x-data-version":    {},
	"content-type":      {},
	"x-install-id":      {},
}

func GetAPIEndpoint() map[harukiUtils.SupportedDataUploadServer][2]string {
	return map[harukiUtils.SupportedDataUploadServer][2]string{
		harukiUtils.SupportedDataUploadServerJP: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.JPServerAPIHost), harukiConfig.Cfg.SekaiClient.JPServerAPIHost},
		harukiUtils.SupportedDataUploadServerEN: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.ENServerAPIHost), harukiConfig.Cfg.SekaiClient.ENServerAPIHost},
		harukiUtils.SupportedDataUploadServerTW: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.TWServerAPIHost), harukiConfig.Cfg.SekaiClient.TWServerAPIHost},
		harukiUtils.SupportedDataUploadServerKR: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.KRServerAPIHost), harukiConfig.Cfg.SekaiClient.KRServerAPIHost},
		harukiUtils.SupportedDataUploadServerCN: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.CNServerAPIHost), harukiConfig.Cfg.SekaiClient.CNServerAPIHost},
	}
}

var acquirePath = map[harukiUtils.UploadDataType]string{
	harukiUtils.UploadDataTypeSuite:   "/suite/user/%d",
	harukiUtils.UploadDataTypeMysekai: "/user/%d/mysekai?isForceAllReloadOnlyMySekai=True",
}

func cleanSuite(suite map[string]interface{}) map[string]interface{} {
	removeKeys := harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys
	for _, key := range removeKeys {
		if _, ok := suite[key]; ok {
			suite[key] = []interface{}{}
		}
	}
	return suite
}

func filterHeaders(headers map[string]string) map[string]string {
	filtered := make(map[string]string)
	for k, v := range headers {
		kl := strings.ToLower(k)
		if _, ok := allowedHeaders[kl]; ok {
			filtered[kl] = v
		}
	}
	return filtered
}

func HarukiSekaiProxyCallAPI(
	ctx context.Context,
	headers map[string]string,
	method string,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	policy harukiUtils.UploadPolicy,
	data []byte,
	params map[string]string,
	proxy string,
	userID int64,
	preHandle func(map[string]interface{}, int64, harukiUtils.UploadPolicy, harukiUtils.SupportedDataUploadServer) map[string]interface{},
) (*harukiUtils.SekaiDataRetrieverResponse, error) {

	apiEndpoint := GetAPIEndpoint()
	endpoint, ok := apiEndpoint[server]
	if !ok {
		return nil, fmt.Errorf("invalid server: %s", server)
	}
	baseURL, host := endpoint[0], endpoint[1]

	filteredHeaders := filterHeaders(headers)
	filteredHeaders["Host"] = host

	url := fmt.Sprintf(baseURL+acquirePath[dataType], userID)

	if params != nil && len(params) > 0 {
		q := urlParse.Values{}
		for k, v := range params {
			q.Set(k, v)
		}
		url += "?" + q.Encode()
	}

	client := harukiHttp.NewClient(proxy, 15*time.Second)

	statusCode, respHeaders, respBody, err := client.Request(ctx, method, url, filteredHeaders, data)
	if err != nil {
		return nil, err
	}

	rawBody := append([]byte(nil), respBody...)
	unpacked, err := Unpack(rawBody, server)
	if err != nil {
		return nil, err
	}

	unpackedMap := preHandle(unpacked.(map[string]interface{}), userID, policy, server)
	if dataType == harukiUtils.UploadDataTypeSuite {
		unpackedMap = cleanSuite(unpackedMap)
	}

	newHeaders := make(map[string]string)
	for k, v := range respHeaders {
		newHeaders[k] = v
	}

	return &harukiUtils.SekaiDataRetrieverResponse{
		RawBody:       rawBody,
		DecryptedBody: unpackedMap,
		StatusCode:    statusCode,
		NewHeaders:    newHeaders,
	}, nil
}

func HandleProxyUpload(
	manager *harukiMongo.MongoDBManager,
	proxy string,
	policy harukiUtils.UploadPolicy,
	preHandle func(map[string]interface{}, int64, harukiUtils.UploadPolicy, harukiUtils.SupportedDataUploadServer) map[string]interface{},
	callWebhook func(context.Context, int64, harukiUtils.SupportedDataUploadServer, harukiUtils.UploadDataType),
	redisClient *redis.Client,
	dataType harukiUtils.UploadDataType,
) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := context.Background()

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
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}

		headers := make(map[string]string)
		for k, v := range c.Request().Header.All() {
			headers[string(append([]byte(nil), k...))] = string(append([]byte(nil), v...))
		}

		var body []byte
		if c.Method() == fiber.MethodPost {
			body = c.Body()
		}

		params := c.Queries()
		resp, err := HarukiSekaiProxyCallAPI(
			ctx,
			headers,
			c.Method(),
			server,
			dataType,
			policy,
			body,
			params,
			proxy,
			userID,
			preHandle,
		)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		go manager.UpdateData(ctx, string(server), userID, resp.DecryptedBody, dataType)

		go harukiRedis.ClearCache(ctx, redisClient, string(dataType), string(server), userID)
		go callWebhook(ctx, userID, server, dataType)

		for k, v := range resp.NewHeaders {
			c.Set(k, v)
		}
		return c.Status(resp.StatusCode).Send(resp.RawBody)
	}
}
