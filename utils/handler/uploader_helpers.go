package handler

import (
	"fmt"
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
	apiHelper "haruki-suite/utils/api"
	"strconv"
	"strings"
)

const (
	headerAuthorization      = "Authorization"
	headerXCredentials       = "X-Credentials"
	headerXServerRegion      = "X-Server-Region"
	headerXUploadType        = "X-Upload-Type"
	headerXUserID            = "X-User-Id"
	headerXUploadDataFormat  = "X-Haruki-Upload-Data-Format"
	authBearerFormat         = "Bearer %s"
	urlPlaceholderUserID     = "{user_id}"
	urlPlaceholderServer     = "{server}"
	urlPlaceholderDataType   = "{data_type}"
	httpStatusOK             = 200
	httpStatusNotFound       = 404
	dataSyncerTimeoutSeconds = 30
	defaultAcceptOctetStream = "application/octet-stream"
	defaultUserAgentName     = "Haruki-Toolbox-Backend/%s"
)

type syncTarget struct {
	url               string
	secret            string
	sendJSONZstandard bool
	is8823            bool
	checkEnabled      bool
	checkURL          string
	restoreSuite      bool
}

func buildSyncTargets(
	cfg harukiConfig.ThirdPartyDataProviderConfig,
	dataType utils.UploadDataType,
	settings apiHelper.HarukiToolboxGameAccountPrivacySettings,
) []syncTarget {
	var targets []syncTarget

	if dataType == utils.UploadDataTypeSuite && settings.Suite != nil {
		if settings.Suite.Allow8823 {
			targets = append(targets, syncTarget{
				url:               cfg.Endpoint8823,
				secret:            cfg.Secret8823,
				sendJSONZstandard: cfg.SendJSONZstandard8823,
				is8823:            true,
				checkEnabled:      cfg.CheckEnabled8823,
				checkURL:          cfg.CheckURL8823,
				restoreSuite:      cfg.RestoreSuite8823,
			})
		}
		if settings.Suite.AllowSakura {
			targets = append(targets, syncTarget{
				url:               cfg.EndpointSakura,
				secret:            cfg.SecretSakura,
				sendJSONZstandard: cfg.SendJSONZstandardSakura,
				checkEnabled:      cfg.CheckEnabledSakura,
				checkURL:          cfg.CheckURLSakura,
				restoreSuite:      cfg.RestoreSuiteSakura,
			})
		}
		if settings.Suite.AllowResona {
			targets = append(targets, syncTarget{
				url:               cfg.EndpointResona,
				secret:            cfg.SecretResona,
				sendJSONZstandard: cfg.SendJSONZstandardResona,
				checkEnabled:      cfg.CheckEnabledResona,
				checkURL:          cfg.CheckURLResona,
				restoreSuite:      cfg.RestoreSuiteResona,
			})
		}
		if settings.Suite.AllowLuna {
			targets = append(targets, syncTarget{
				url:               cfg.EndpointLuna,
				secret:            cfg.SecretLuna,
				sendJSONZstandard: cfg.SendJSONZstandardLuna,
				checkEnabled:      cfg.CheckEnabledLuna,
				checkURL:          cfg.CheckURLLuna,
				restoreSuite:      cfg.RestoreSuiteLuna,
			})
		}
	}

	if (dataType == utils.UploadDataTypeMysekai || dataType == utils.UploadDataTypeMysekaiBirthdayParty) && settings.Mysekai != nil {
		if settings.Mysekai.Allow8823 {
			targets = append(targets, syncTarget{
				url:               cfg.Endpoint8823,
				secret:            cfg.Secret8823,
				sendJSONZstandard: cfg.SendJSONZstandard8823,
				is8823:            true,
				checkEnabled:      cfg.CheckEnabled8823,
				checkURL:          cfg.CheckURL8823,
			})
		}
		if settings.Mysekai.AllowResona {
			targets = append(targets, syncTarget{
				url:               cfg.EndpointResona,
				secret:            cfg.SecretResona,
				sendJSONZstandard: cfg.SendJSONZstandardResona,
				checkEnabled:      cfg.CheckEnabledResona,
				checkURL:          cfg.CheckURLResona,
			})
		}
		if settings.Mysekai.AllowLuna {
			targets = append(targets, syncTarget{
				url:               cfg.EndpointLuna,
				secret:            cfg.SecretLuna,
				sendJSONZstandard: cfg.SendJSONZstandardLuna,
				checkEnabled:      cfg.CheckEnabledLuna,
				checkURL:          cfg.CheckURLLuna,
			})
		}
	}

	return targets
}

func computeProcessingNeeds(targets []syncTarget, dataType utils.UploadDataType) (bool, bool) {
	needsProcessed := false
	needsRestored := false

	for _, target := range targets {
		if !target.sendJSONZstandard {
			continue
		}
		if target.restoreSuite && dataType == utils.UploadDataTypeSuite {
			needsRestored = true
			continue
		}
		needsProcessed = true
	}

	return needsProcessed, needsRestored
}

func chooseSyncPayload(
	target syncTarget,
	dataType utils.UploadDataType,
	rawData []byte,
	processedData []byte,
	restoredData []byte,
	needsProcessed bool,
	needsRestored bool,
) ([]byte, string) {
	if target.sendJSONZstandard && target.restoreSuite && dataType == utils.UploadDataTypeSuite && needsRestored {
		return restoredData, utils.HarukiDataSyncerDataFormatJsonZstd
	}
	if target.sendJSONZstandard && needsProcessed {
		return processedData, utils.HarukiDataSyncerDataFormatJsonZstd
	}
	return rawData, utils.HarukiDataSyncerDataFormatRaw
}

func buildSyncHeaders(
	target syncTarget,
	userID int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
) map[string]string {
	if target.is8823 {
		return map[string]string{
			headerXCredentials:  target.secret,
			headerXServerRegion: string(server),
			headerXUploadType:   string(dataType),
			headerXUserID:       strconv.FormatInt(userID, 10),
		}
	}
	return map[string]string{
		headerAuthorization: fmt.Sprintf(authBearerFormat, target.secret),
	}
}

func buildCheckHeaders(target syncTarget) map[string]string {
	if target.is8823 {
		return map[string]string{
			headerXCredentials: target.secret,
		}
	}
	return map[string]string{
		headerAuthorization: fmt.Sprintf(authBearerFormat, target.secret),
	}
}

func replaceSyncURLPlaceholders(
	url string,
	userID int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
) string {
	replaced := strings.ReplaceAll(url, urlPlaceholderUserID, strconv.FormatInt(userID, 10))
	replaced = strings.ReplaceAll(replaced, urlPlaceholderServer, string(server))
	replaced = strings.ReplaceAll(replaced, urlPlaceholderDataType, string(dataType))
	return replaced
}
