package sekai

import (
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiHttp "haruki-suite/utils/http"
	"maps"
	urlParse "net/url"
	"strings"
	"time"
)

var apiEndpoints = map[harukiUtils.SupportedDataUploadServer][2]string{
	harukiUtils.SupportedDataUploadServerJP: {
		fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.JPServerAPIHost),
		harukiConfig.Cfg.SekaiClient.JPServerAPIHost,
	},
	harukiUtils.SupportedDataUploadServerEN: {
		fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.ENServerAPIHost),
		harukiConfig.Cfg.SekaiClient.ENServerAPIHost,
	},
	harukiUtils.SupportedDataUploadServerTW: {
		fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.TWServerAPIHost),
		harukiConfig.Cfg.SekaiClient.TWServerAPIHost,
	},
	harukiUtils.SupportedDataUploadServerKR: {
		fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.KRServerAPIHost),
		harukiConfig.Cfg.SekaiClient.KRServerAPIHost,
	},
	harukiUtils.SupportedDataUploadServerCN: {
		fmt.Sprintf("https://%s/api", harukiConfig.Cfg.SekaiClient.CNServerAPIHost),
		harukiConfig.Cfg.SekaiClient.CNServerAPIHost,
	},
}

func GetAPIEndpoint() map[harukiUtils.SupportedDataUploadServer][2]string {
	return apiEndpoints
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
	mysekaiBirthdayPartyID *int64,
) (*harukiUtils.SekaiDataRetrieverResponse, error) {
	if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty {
		if mysekaiBirthdayPartyID == nil || *mysekaiBirthdayPartyID == 0 {
			return nil, NewAPIError("/birthday-party", method, 0,
				"birthday party ID is required but was not provided", nil)
		}
	}
	endpoint, ok := apiEndpoints[server]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInvalidServer, server)
	}
	baseURL, host := endpoint[0], endpoint[1]
	pathTemplate, ok := acquirePath[dataType]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInvalidDataType, dataType)
	}
	filteredHeaders := filterHeaders(headers)
	filteredHeaders["Host"] = host
	var url string
	if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty {
		url = baseURL + fmt.Sprintf(pathTemplate, userID, *mysekaiBirthdayPartyID)
	} else {
		url = baseURL + fmt.Sprintf(pathTemplate, userID)
	}
	if len(params) > 0 {
		q := urlParse.Values{}
		for k, v := range params {
			q.Set(k, v)
		}
		url += "?" + q.Encode()
	}
	client := harukiHttp.NewClient(proxy, 30*time.Second)
	statusCode, respHeaders, respBody, err := client.Request(ctx, method, url, filteredHeaders, data)
	if err != nil {
		return nil, NewAPIError(url, method, 0, "HTTP request failed", err)
	}
	rawBody := make([]byte, len(respBody))
	copy(rawBody, respBody)
	newHeaders := make(map[string]string, len(respHeaders))
	maps.Copy(newHeaders, respHeaders)
	return &harukiUtils.SekaiDataRetrieverResponse{
		RawBody:    rawBody,
		StatusCode: statusCode,
		NewHeaders: newHeaders,
	}, nil
}
