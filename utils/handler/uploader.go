package handler

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
	apiHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"
	harukiVersion "haruki-suite/version"
	"strconv"
	"strings"

	"github.com/go-resty/resty/v2"
)

var logger = harukiLogger.NewLogger("HarukiDataSyncer", "DEBUG", nil)

func DataUploader(url string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, rawData []byte, endpointSecret string) {
	if url != "" {
		url = strings.ReplaceAll(url, "{user_id}", fmt.Sprint(userID))
		url = strings.ReplaceAll(url, "{server}", string(server))
		url = strings.ReplaceAll(url, "{data_type}", string(dataType))
		httpClient := resty.New()
		resp, err := httpClient.R().
			SetHeader("User-Agent", fmt.Sprintf("Haruki-Toolbox-Backend/%s", harukiVersion.Version)).
			SetHeader("Accept", "application/octet-stream").
			SetHeader("Authorization", fmt.Sprintf("Bearer %s", endpointSecret)).
			SetBody(rawData).
			Post(url)
		if err != nil {
			logger.Warnf("Failed to sync data to %s: %v", url, err)
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

func Sync8823(url string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, rawData []byte, endpointSecret string) {
	if url != "" {
		httpClient := resty.New()
		resp, err := httpClient.R().
			SetHeader("User-Agent", fmt.Sprintf("Haruki-Toolbox-Backend/%s", harukiVersion.Version)).
			SetHeader("Accept", "application/octet-stream").
			SetHeader("X-Credentials", endpointSecret).
			SetHeader("X-Server-Region", string(server)).
			SetHeader("X-Upload-Type", string(dataType)).
			SetHeader("X-User-Id", strconv.FormatInt(userID, 10)).
			SetBody(rawData).
			Post(url)
		if err != nil {
			logger.Warnf("Failed to sync data to %s: %v", url, err)
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
	if dataType == utils.UploadDataTypeSuite {
		if settings.Suite != nil {
			if settings.Suite.Allow8823 {
				go Sync8823(harukiConfig.Cfg.ThirdPartyDataProvider.Endpoint8823, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.Secret8823)
				logger.Infof("Syncing suite data to 8823...")
			}
			if settings.Suite.AllowSakura {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointSakura, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretSakura)
				logger.Infof("Syncing suite data to SakuraBot...")
			}
			if settings.Suite.AllowResona {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointResona, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretResona)
				logger.Infof("Syncing suite data to ResonaBot...")
			}
			if settings.Suite.AllowLuna {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointLuna, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretLuna)
				logger.Infof("Syncing suite data to LunaBot...")
			}
		}
	}
	if dataType == utils.UploadDataTypeMysekai || dataType == utils.UploadDataTypeMysekaiBirthdayParty {
		if settings.Mysekai != nil {
			if settings.Mysekai.Allow8823 {
				go Sync8823(harukiConfig.Cfg.ThirdPartyDataProvider.Endpoint8823, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.Secret8823)
				logger.Infof("Syncing mysekai data to 8823...")
			}
			if settings.Mysekai.AllowResona {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointResona, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretResona)
				logger.Infof("Syncing mysekai data to ResonaBot...")
			}
			if settings.Mysekai.AllowLuna {
				go DataUploader(harukiConfig.Cfg.ThirdPartyDataProvider.EndpointLuna, userID, server, dataType, rawData, harukiConfig.Cfg.ThirdPartyDataProvider.SecretLuna)
				logger.Infof("Syncing mysekai data to LunaBot...")
			}
		}
	}
}
