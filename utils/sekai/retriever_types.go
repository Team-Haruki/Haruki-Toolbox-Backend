package sekai

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
)

type DataRetriever struct {
	client       *HarukiSekaiClient
	uploadType   harukiUtils.UploadDataType
	logger       *harukiLogger.Logger
	isErrorExist bool
	ErrorMessage string
}

type HarukiSekaiDataRetriever = DataRetriever

func NewSekaiDataRetriever(
	server harukiUtils.SupportedInheritUploadServer,
	inherit harukiUtils.InheritInformation,
	uploadType harukiUtils.UploadDataType,
) *HarukiSekaiDataRetriever {
	client, err := newRetrieverClient(server, inherit)
	if err != nil {
		logger := harukiLogger.NewLogger("SekaiDataRetriever", "DEBUG", nil)
		msg := fmt.Sprintf("failed to build retriever client: %v", err)
		logger.Errorf("%s", msg)
		return &HarukiSekaiDataRetriever{
			uploadType:   uploadType,
			logger:       logger,
			isErrorExist: true,
			ErrorMessage: msg,
		}
	}

	return &HarukiSekaiDataRetriever{
		client:       client,
		uploadType:   uploadType,
		logger:       harukiLogger.NewLogger("SekaiDataRetriever", "DEBUG", nil),
		isErrorExist: false,
		ErrorMessage: "",
	}
}

func newRetrieverClient(
	server harukiUtils.SupportedInheritUploadServer,
	inherit harukiUtils.InheritInformation,
) (*HarukiSekaiClient, error) {
	serverConfig, err := GetServerConfig(server)
	if err != nil {
		return nil, err
	}

	return NewSekaiClientWithConfig(ClientConfig{
		Server:          server,
		API:             serverConfig.APIEndpoint,
		VersionURL:      serverConfig.AppVersionURL,
		Inherit:         inherit,
		Headers:         serverConfig.Headers,
		Proxy:           harukiConfig.Cfg.Proxy,
		InheritJWTToken: serverConfig.InheritJWTToken,
	}), nil
}

func (r *HarukiSekaiDataRetriever) ensureReady(dataType, step string) error {
	if r.isErrorExist {
		return NewDataRetrievalError(dataType, step, r.ErrorMessage, nil)
	}
	if r.client == nil {
		return NewDataRetrievalError(dataType, step, "client is nil", nil)
	}
	return nil
}
