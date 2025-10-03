package sekai

import (
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiHttp "haruki-suite/utils/http"
	urlParse "net/url"
	"strings"
	"time"
)

func GetAPIEndpoint() map[harukiUtils.SupportedDataUploadServer][2]string {
	return map[harukiUtils.SupportedDataUploadServer][2]string{
		harukiUtils.SupportedDataUploadServerJP: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.JPServerAPIHost), harukiConfig.Cfg.SekaiClient.JPServerAPIHost},
		harukiUtils.SupportedDataUploadServerEN: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.ENServerAPIHost), harukiConfig.Cfg.SekaiClient.ENServerAPIHost},
		harukiUtils.SupportedDataUploadServerTW: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.TWServerAPIHost), harukiConfig.Cfg.SekaiClient.TWServerAPIHost},
		harukiUtils.SupportedDataUploadServerKR: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.KRServerAPIHost), harukiConfig.Cfg.SekaiClient.KRServerAPIHost},
		harukiUtils.SupportedDataUploadServerCN: {fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.CNServerAPIHost), harukiConfig.Cfg.SekaiClient.CNServerAPIHost},
	}
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
	data []byte,
	params map[string]string,
	proxy string,
	userID int64,
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

	newHeaders := make(map[string]string)
	for k, v := range respHeaders {
		newHeaders[k] = v
	}

	return &harukiUtils.SekaiDataRetrieverResponse{
		RawBody:    rawBody,
		StatusCode: statusCode,
		NewHeaders: newHeaders,
	}, nil
}
