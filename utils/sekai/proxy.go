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

var proxyPathByUploadType = map[harukiUtils.UploadDataType]string{
	harukiUtils.UploadDataTypeSuite:                "/suite/user/%d",
	harukiUtils.UploadDataTypeMysekai:              "/user/%d/mysekai",
	harukiUtils.UploadDataTypeMysekaiBirthdayParty: "/user/%d/mysekai/birthday-party/%d/delivery",
}

var proxyAllowedHeaderSet = map[string]struct{}{
	"user-agent":        {},
	"cookie":            {},
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
	return buildAPIEndpoints()
}

func buildAPIEndpoints() map[harukiUtils.SupportedDataUploadServer][2]string {
	cfg := harukiConfig.Cfg.SekaiClient
	return map[harukiUtils.SupportedDataUploadServer][2]string{
		harukiUtils.SupportedDataUploadServerJP: {
			fmt.Sprintf("https://%s/api", cfg.JPServerAPIHost),
			cfg.JPServerAPIHost,
		},
		harukiUtils.SupportedDataUploadServerEN: {
			fmt.Sprintf("https://%s/api", cfg.ENServerAPIHost),
			cfg.ENServerAPIHost,
		},
		harukiUtils.SupportedDataUploadServerTW: {
			fmt.Sprintf("https://%s/api", cfg.TWServerAPIHost),
			cfg.TWServerAPIHost,
		},
		harukiUtils.SupportedDataUploadServerKR: {
			fmt.Sprintf("https://%s/api", cfg.KRServerAPIHost),
			cfg.KRServerAPIHost,
		},
		harukiUtils.SupportedDataUploadServerCN: {
			fmt.Sprintf("https://%s/api", cfg.CNServerAPIHost),
			cfg.CNServerAPIHost,
		},
	}
}

func filterHeaders(headers map[string]string) map[string]string {
	filtered := make(map[string]string)
	for k, v := range headers {
		kl := strings.ToLower(k)
		if _, ok := proxyAllowedHeaderSet[kl]; ok {
			filtered[kl] = v
		}
	}
	return filtered
}

func resolveProxyEndpoint(server harukiUtils.SupportedDataUploadServer) (baseURL, host string, err error) {
	endpoint, ok := GetAPIEndpoint()[server]
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrInvalidServer, server)
	}
	return endpoint[0], endpoint[1], nil
}

func buildProxyPath(
	dataType harukiUtils.UploadDataType,
	method string,
	userID int64,
	mysekaiBirthdayPartyID *int64,
) (string, error) {
	pathTemplate, ok := proxyPathByUploadType[dataType]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrInvalidDataType, dataType)
	}
	if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty {
		if mysekaiBirthdayPartyID == nil || *mysekaiBirthdayPartyID == 0 {
			return "", NewAPIError(
				"/birthday-party",
				method,
				0,
				"birthday party ID is required but was not provided",
				nil,
			)
		}
		return fmt.Sprintf(pathTemplate, userID, *mysekaiBirthdayPartyID), nil
	}
	return fmt.Sprintf(pathTemplate, userID), nil
}

func appendQueryParams(url string, params map[string]string) string {
	if len(params) == 0 {
		return url
	}
	q := urlParse.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	return url + "?" + q.Encode()
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
	baseURL, host, err := resolveProxyEndpoint(server)
	if err != nil {
		return nil, err
	}
	path, err := buildProxyPath(dataType, method, userID, mysekaiBirthdayPartyID)
	if err != nil {
		return nil, err
	}
	filteredHeaders := filterHeaders(headers)
	filteredHeaders["Host"] = host
	url := appendQueryParams(baseURL+path, params)
	client := harukiHttp.NewClient(proxy, 30*time.Second)
	statusCode, respHeaders, respBody, err := client.RequestWithHeaders(ctx, method, url, filteredHeaders, data)
	if err != nil {
		return nil, NewAPIError(url, method, 0, "HTTP request failed", err)
	}
	rawBody := make([]byte, len(respBody))
	copy(rawBody, respBody)
	newHeaders := make(map[string][]string, len(respHeaders))
	maps.Copy(newHeaders, respHeaders)
	return &harukiUtils.SekaiDataRetrieverResponse{
		RawBody:    rawBody,
		StatusCode: statusCode,
		NewHeaders: newHeaders,
	}, nil
}
