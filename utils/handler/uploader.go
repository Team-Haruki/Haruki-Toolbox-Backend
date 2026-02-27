package handler

import (
	"bytes"
	"fmt"
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
	apiHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekai"
	"haruki-suite/utils/streamjson"
	harukiVersion "haruki-suite/version"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
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

var zstdEncoderPool = sync.Pool{
	New: func() any {
		w, _ := zstd.NewWriter(nil)
		return w
	},
}

func processDataOnce(rawData []byte, server utils.SupportedDataUploadServer) ([]byte, error) {

	msgpackBytes, err := sekai.DecryptToMsgpack(rawData, server)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	var buf bytes.Buffer
	encoder := zstdEncoderPool.Get().(*zstd.Encoder)
	encoder.Reset(&buf)

	if err := streamjson.Convert(msgpackBytes, encoder); err != nil {
		encoder.Close()
		zstdEncoderPool.Put(encoder)
		return nil, fmt.Errorf("failed to stream convert msgpack to json+zstd: %w", err)
	}

	msgpackBytes = nil

	if err := encoder.Close(); err != nil {
		zstdEncoderPool.Put(encoder)
		return nil, fmt.Errorf("failed to close zstd writer: %w", err)
	}
	zstdEncoderPool.Put(encoder)

	return buf.Bytes(), nil
}

func processDataWithRestore(rawData []byte, server utils.SupportedDataUploadServer) ([]byte, error) {
	unpacked, err := sekai.Unpack(rawData, server)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack data: %w", err)
	}
	unpackedMap, ok := unpacked.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unpacked data is not a map")
	}

	if r := getSuiteRestorer(server); r != nil {
		r.RestoreFields(unpackedMap)
	}

	jsonBytes, err := sonic.Marshal(unpackedMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal restored data to json: %w", err)
	}

	var buf bytes.Buffer
	encoder := zstdEncoderPool.Get().(*zstd.Encoder)
	encoder.Reset(&buf)

	if _, err := encoder.Write(jsonBytes); err != nil {
		encoder.Close()
		zstdEncoderPool.Put(encoder)
		return nil, fmt.Errorf("failed to write json to zstd encoder: %w", err)
	}

	jsonBytes = nil

	if err := encoder.Close(); err != nil {
		zstdEncoderPool.Put(encoder)
		return nil, fmt.Errorf("failed to close zstd writer: %w", err)
	}
	zstdEncoderPool.Put(encoder)

	return buf.Bytes(), nil
}

func sendData(url string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, data []byte, encoding string, endpointSecret string, headers map[string]string) {
	if url == "" {
		logger.Warnf("Upload endpoint url is empty, skipped syncing data.")
		return
	}

	url = strings.ReplaceAll(url, "{user_id}", fmt.Sprint(userID))
	url = strings.ReplaceAll(url, "{server}", string(server))
	url = strings.ReplaceAll(url, "{data_type}", string(dataType))

	req := httpClient.R().
		SetHeader("X-Haruki-Upload-Data-Format", encoding).
		SetBody(data)

	for k, v := range headers {
		req.SetHeader(k, v)
	}

	resp, err := req.Post(url)
	if err != nil {
		logger.Warnf("Failed to sync data to %s: %v", url, err)
		return
	}
	if resp.StatusCode() != 200 {
		logger.Warnf("Failed to sync data to %s: status code %v", url, resp.Status())
	} else {
		logger.Infof("Successfully sync data to %s", url)
	}
}

type syncTarget struct {
	url               string
	secret            string
	sendJSONZstandard bool
	is8823            bool
	checkEnabled      bool
	checkURL          string
	restoreSuite      bool
}

func checkUserExists(t syncTarget, userID int64, server utils.SupportedDataUploadServer) bool {
	if !t.checkEnabled || t.checkURL == "" {
		return true
	}

	url := t.checkURL
	url = strings.ReplaceAll(url, "{user_id}", strconv.FormatInt(userID, 10))
	url = strings.ReplaceAll(url, "{server}", string(server))

	req := httpClient.R()
	if t.is8823 {
		req.SetHeader("X-Credentials", t.secret)
	} else {
		req.SetHeader("Authorization", fmt.Sprintf("Bearer %s", t.secret))
	}

	resp, err := req.Get(url)
	if err != nil {
		logger.Warnf("Check user failed for %s: %v", url, err)
		return false
	}
	if resp.StatusCode() == 200 {
		return true
	}
	if resp.StatusCode() == 404 {
		logger.Debugf("User %d not found at %s, skipping sync", userID, url)
		return false
	}
	logger.Warnf("Unexpected check response from %s: %d", url, resp.StatusCode())
	return false
}

func DataSyncer(userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, rawData []byte, settings apiHelper.HarukiToolboxGameAccountPrivacySettings) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("DataSyncer panicked: %v", r)
		}
	}()

	cfg := harukiConfig.Cfg.ThirdPartyDataProvider

	var targets []syncTarget

	if dataType == utils.UploadDataTypeSuite {
		if settings.Suite != nil {
			if settings.Suite.Allow8823 {
				targets = append(targets, syncTarget{cfg.Endpoint8823, cfg.Secret8823, cfg.SendJSONZstandard8823, true, cfg.CheckEnabled8823, cfg.CheckURL8823, cfg.RestoreSuite8823})
			}
			if settings.Suite.AllowSakura {
				targets = append(targets, syncTarget{cfg.EndpointSakura, cfg.SecretSakura, cfg.SendJSONZstandardSakura, false, cfg.CheckEnabledSakura, cfg.CheckURLSakura, cfg.RestoreSuiteSakura})
			}
			if settings.Suite.AllowResona {
				targets = append(targets, syncTarget{cfg.EndpointResona, cfg.SecretResona, cfg.SendJSONZstandardResona, false, cfg.CheckEnabledResona, cfg.CheckURLResona, cfg.RestoreSuiteResona})
			}
			if settings.Suite.AllowLuna {
				targets = append(targets, syncTarget{cfg.EndpointLuna, cfg.SecretLuna, cfg.SendJSONZstandardLuna, false, cfg.CheckEnabledLuna, cfg.CheckURLLuna, cfg.RestoreSuiteLuna})
			}
		}
	}
	if dataType == utils.UploadDataTypeMysekai || dataType == utils.UploadDataTypeMysekaiBirthdayParty {
		if settings.Mysekai != nil {
			if settings.Mysekai.Allow8823 {
				targets = append(targets, syncTarget{cfg.Endpoint8823, cfg.Secret8823, cfg.SendJSONZstandard8823, true, cfg.CheckEnabled8823, cfg.CheckURL8823, false})
			}
			if settings.Mysekai.AllowResona {
				targets = append(targets, syncTarget{cfg.EndpointResona, cfg.SecretResona, cfg.SendJSONZstandardResona, false, cfg.CheckEnabledResona, cfg.CheckURLResona, false})
			}
			if settings.Mysekai.AllowLuna {
				targets = append(targets, syncTarget{cfg.EndpointLuna, cfg.SecretLuna, cfg.SendJSONZstandardLuna, false, cfg.CheckEnabledLuna, cfg.CheckURLLuna, false})
			}
		}
	}

	if len(targets) == 0 {
		return
	}

	needsProcessed := false
	needsRestored := false
	for _, t := range targets {
		if t.sendJSONZstandard {
			if t.restoreSuite && dataType == utils.UploadDataTypeSuite {
				needsRestored = true
			} else {
				needsProcessed = true
			}
		}
	}

	var processedData []byte
	if needsProcessed {
		var err error
		processedData, err = processDataOnce(rawData, server)
		if err != nil {
			logger.Warnf("Failed to pre-process data: %v", err)
			needsProcessed = false
		}
	}

	var restoredData []byte
	if needsRestored {
		var err error
		restoredData, err = processDataWithRestore(rawData, server)
		if err != nil {
			logger.Warnf("Failed to process data with restore: %v", err)
			needsRestored = false
		}
	}

	for _, t := range targets {
		t := t

		if !checkUserExists(t, userID, server) {
			logger.Infof("Skipping sync to %s: user %d not found", t.url, userID)
			continue
		}

		var data []byte
		var encoding string
		if t.sendJSONZstandard && t.restoreSuite && dataType == utils.UploadDataTypeSuite && needsRestored {
			data = restoredData
			encoding = utils.HarukiDataSyncerDataFormatJsonZstd
		} else if t.sendJSONZstandard && needsProcessed {
			data = processedData
			encoding = utils.HarukiDataSyncerDataFormatJsonZstd
		} else {
			data = rawData
			encoding = utils.HarukiDataSyncerDataFormatRaw
		}

		var headers map[string]string
		if t.is8823 {
			headers = map[string]string{
				"X-Credentials":   t.secret,
				"X-Server-Region": string(server),
				"X-Upload-Type":   string(dataType),
				"X-User-Id":       strconv.FormatInt(userID, 10),
			}
		} else {
			headers = map[string]string{
				"Authorization": fmt.Sprintf("Bearer %s", t.secret),
			}
		}

		logger.Infof("Syncing %s data to %s...", dataType, t.url)
		go sendData(t.url, userID, server, dataType, data, encoding, t.secret, headers)
	}
}
