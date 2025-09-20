package sekai

import (
	"bytes"
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiMongo "haruki-suite/utils/mongo"
	"io"
	"net/http"
	urlParse "net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
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

func getAPIEndpoint() map[harukiUtils.SupportedDataUploadServer][2]string {
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
	preHandle func(map[string]interface{}, int64, harukiUtils.UploadPolicy, string) map[string]interface{},
) (*harukiUtils.SekaiDataRetrieverResponse, error) {

	apiEndpoint := getAPIEndpoint()
	endpoint, ok := apiEndpoint[server]
	if !ok {
		return nil, fmt.Errorf("invalid server: %s", server)
	}
	baseURL, host := endpoint[0], endpoint[1]

	filteredHeaders := filterHeaders(headers)
	filteredHeaders["Host"] = host

	url := fmt.Sprintf(baseURL+acquirePath[dataType], userID)

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if params != nil {
		q := req.URL.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	for k, v := range filteredHeaders {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	if proxy != "" {
		proxyURL, err := urlParse.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url: %v", err)
		}
		client.Transport = &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	unpacked, err := Unpack(rawBody, server)
	if err != nil {
		return nil, err
	}

	unpackedMap := preHandle(unpacked.(map[string]interface{}), userID, policy, string(server))

	if dataType == harukiUtils.UploadDataTypeSuite {
		unpackedMap = cleanSuite(unpackedMap)
	}

	newHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			newHeaders[k] = v[0]
		}
	}

	return &harukiUtils.SekaiDataRetrieverResponse{
		RawBody:       rawBody,
		DecryptedBody: unpackedMap,
		StatusCode:    resp.StatusCode,
		NewHeaders:    newHeaders,
	}, nil
}

func HandleProxyUpload(
	manager *harukiMongo.MongoDBManager,
	proxy string,
	policy harukiUtils.UploadPolicy,
	preHandle func(map[string]interface{}, int64, harukiUtils.UploadPolicy, string) map[string]interface{},
	callWebhook func(context.Context, int64, string, harukiUtils.UploadDataType, *harukiMongo.MongoDBManager),
) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := context.Background()

		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		userID, err := c.ParamsInt("user_id")
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid user_id")
		}

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}

		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}

		headers := make(map[string]string)
		c.Request().Header.VisitAll(func(k, v []byte) {
			headers[string(k)] = string(v)
		})

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
			int64(userID),
			preHandle,
		)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		if _, err := manager.UpdateData(ctx, int64(userID), resp.DecryptedBody, dataType); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		go callWebhook(ctx, int64(userID), string(server), dataType, manager)

		for k, v := range resp.NewHeaders {
			c.Set(k, v)
		}
		return c.Status(resp.StatusCode).Send(resp.RawBody)
	}
}
