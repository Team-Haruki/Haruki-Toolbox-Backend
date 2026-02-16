package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
	apiHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekai"
	harukiVersion "haruki-suite/version"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/klauspost/compress/zstd"
)

var (
	logger     = harukiLogger.NewLogger("HarukiDataSyncer", "DEBUG", nil)
	httpClient *resty.Client
)

func init() {
	httpClient = resty.New()
	httpClient.SetTimeout(30 * time.Second)
	httpClient.SetHeader("User-Agent", fmt.Sprintf("Haruki-Toolbox-Backend/%s", harukiVersion.Version))
	httpClient.SetHeader("Accept", "application/octet-stream")
}

func processData(rawData []byte, server utils.SupportedDataUploadServer, sendJSONZstandard bool) ([]byte, string, error) {
	if !sendJSONZstandard {
		return rawData, utils.HarukiDataSyncerDataFormatRaw, nil
	}

	unpacked, err := sekai.UnpackOrdered(rawData, server)
	if err != nil {
		return nil, "", fmt.Errorf("failed to unpack ordered data: %w", err)
	}

	jsonData, err := json.Marshal(unpacked)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal json: %w", err)
	}

	var buf bytes.Buffer
	writer, err := zstd.NewWriter(&buf)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create zstd writer: %w", err)
	}
	if _, err := writer.Write(jsonData); err != nil {
		return nil, "", fmt.Errorf("failed to write json to zstd writer: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close zstd writer: %w", err)
	}

	return buf.Bytes(), utils.HarukiDataSyncerDataFormatJsonZstd, nil
}

func DataUploader(url string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, rawData []byte, endpointSecret string, sendJSONZstandard bool) {
	if url != "" {
		dataToSend, encoding, err := processData(rawData, server, sendJSONZstandard)
		if err != nil {
			logger.Warnf("Failed to process data for %s: %v", url, err)
			return
		}

		url = strings.ReplaceAll(url, "{user_id}", fmt.Sprint(userID))
		url = strings.ReplaceAll(url, "{server}", string(server))
		url = strings.ReplaceAll(url, "{data_type}", string(dataType))

		resp, err := httpClient.R().
			SetHeader("Authorization", fmt.Sprintf("Bearer %s", endpointSecret)).
			SetHeader("X-Haruki-Upload-Data-Format", encoding).
			SetBody(dataToSend).
			Post(url)
		if err != nil {
			logger.Warnf("Failed to sync data to %s: %v", url, err)
			return
		}
		if resp.StatusCode() != 200 {
			logger.Warnf("Failed to sync data to %s: status code %v", url, resp.Status())
		} else {
			logger.Infof("Successfully sync data to %s", url)
		}
	} else {
		logger.Warnf("Upload endpoint url is empty, skipped syncing data.")
	}
}

func Sync8823(url string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, rawData []byte, endpointSecret string, sendJSONZstandard bool) {
	if url != "" {
		dataToSend, encoding, err := processData(rawData, server, sendJSONZstandard)
		if err != nil {
			logger.Warnf("Failed to process data for %s: %v", url, err)
			return
		}

		resp, err := httpClient.R().
			SetHeader("X-Credentials", endpointSecret).
			SetHeader("X-Server-Region", string(server)).
			SetHeader("X-Upload-Type", string(dataType)).
			SetHeader("X-User-Id", strconv.FormatInt(userID, 10)).
			SetHeader("X-Haruki-Upload-Data-Format", encoding).
			SetBody(dataToSend).
			Post(url)
		if err != nil {
			logger.Warnf("Failed to sync data to %s: %v", url, err)
			return
		}
		if resp.StatusCode() != 200 {
			logger.Warnf("Failed to sync data to %s: status code %v", url, resp.Status())
		} else {
			logger.Infof("Successfully sync data to %s", url)
		}
	} else {
		logger.Warnf("Upload endpoint url is empty, skipped syncing data.")
	}
}

func DataSyncer(userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, rawData []byte, settings apiHelper.HarukiToolboxGameAccountPrivacySettings) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("DataSyncer panicked: %v", r)
		}
	}()

	if dataType == utils.UploadDataTypeSuite {
		if settings.Suite != nil {
			if settings.Suite.Allow8823 {
				go Sync8823(harukiConfig.Cfg.ThirdPartyDataProvider.Endpoint8823, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.Secret8823, harukiConfig.Cfg.ThirdPartyDataProvider.SendJSONZstandard8823)
				logger.Infof("Syncing suite data to 8823...")
			}
			if settings.Suite.AllowSakura {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointSakura, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretSakura, harukiConfig.Cfg.ThirdPartyDataProvider.SendJSONZstandardSakura)
				logger.Infof("Syncing suite data to SakuraBot...")
			}
			if settings.Suite.AllowResona {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointResona, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretResona, harukiConfig.Cfg.ThirdPartyDataProvider.SendJSONZstandardResona)
				logger.Infof("Syncing suite data to ResonaBot...")
			}
			if settings.Suite.AllowLuna {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointLuna, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretLuna, harukiConfig.Cfg.ThirdPartyDataProvider.SendJSONZstandardLuna)
				logger.Infof("Syncing suite data to LunaBot...")
			}
		}
	}
	if dataType == utils.UploadDataTypeMysekai || dataType == utils.UploadDataTypeMysekaiBirthdayParty {
		if settings.Mysekai != nil {
			if settings.Mysekai.Allow8823 {
				go Sync8823(harukiConfig.Cfg.ThirdPartyDataProvider.Endpoint8823, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.Secret8823, harukiConfig.Cfg.ThirdPartyDataProvider.SendJSONZstandard8823)
				logger.Infof("Syncing mysekai data to 8823...")
			}
			if settings.Mysekai.AllowResona {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointResona, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretResona, harukiConfig.Cfg.ThirdPartyDataProvider.SendJSONZstandardResona)
				logger.Infof("Syncing mysekai data to ResonaBot...")
			}
			if settings.Mysekai.AllowLuna {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointLuna, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretLuna, harukiConfig.Cfg.ThirdPartyDataProvider.SendJSONZstandardLuna)
				logger.Infof("Syncing mysekai data to LunaBot...")
			}
		}
	}
}
