package sekai

import (
	harukiUtils "haruki-suite/utils"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
)

type ClientConfig struct {
	Server          harukiUtils.SupportedInheritUploadServer
	API             string
	VersionURL      string
	Inherit         harukiUtils.InheritInformation
	Headers         map[string]string
	Proxy           string
	InheritJWTToken string
}

func NewSekaiClient(cfg struct {
	Server          harukiUtils.SupportedInheritUploadServer
	API             string
	VersionURL      string
	Inherit         harukiUtils.InheritInformation
	Headers         map[string]string
	Proxy           string
	InheritJWTToken string
}) *HarukiSekaiClient {
	return NewSekaiClientWithConfig(ClientConfig{
		Server:          cfg.Server,
		API:             cfg.API,
		VersionURL:      cfg.VersionURL,
		Inherit:         cfg.Inherit,
		Headers:         cfg.Headers,
		Proxy:           cfg.Proxy,
		InheritJWTToken: cfg.InheritJWTToken,
	})
}

func NewSekaiClientWithConfig(cfg ClientConfig) *HarukiSekaiClient {
	httpClient := harukiHttp.NewClient(cfg.Proxy, defaultSekaiClientTimeout)

	return &HarukiSekaiClient{
		server:          cfg.Server,
		api:             cfg.API,
		versionURL:      cfg.VersionURL,
		inherit:         cfg.Inherit,
		headers:         cloneHeaders(cfg.Headers),
		inheritJWTToken: cfg.InheritJWTToken,
		httpClient:      httpClient,
		logger:          harukiLogger.NewLogger("SekaiClient", "DEBUG", nil),
	}
}

func cloneHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
