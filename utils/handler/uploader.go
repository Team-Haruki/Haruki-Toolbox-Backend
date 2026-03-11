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
	httpClient.SetTimeout(dataSyncerTimeoutSeconds * time.Second)
	httpClient.SetHeader("User-Agent", fmt.Sprintf(defaultUserAgentName, harukiVersion.Version))
	httpClient.SetHeader("Accept", defaultAcceptOctetStream)
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

func sendData(url string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, data []byte, encoding string, headers map[string]string) {
	if url == "" {
		logger.Warnf("Upload endpoint url is empty, skipped syncing data.")
		return
	}

	url = replaceSyncURLPlaceholders(url, userID, server, dataType)

	req := httpClient.R().
		SetHeader(headerXUploadDataFormat, encoding).
		SetBody(data)

	for k, v := range headers {
		req.SetHeader(k, v)
	}

	resp, err := req.Post(url)
	if err != nil {
		logger.Warnf("Failed to sync data to %s: %v", url, err)
		return
	}
	if !isHTTPSuccessStatus(resp.StatusCode()) {
		logger.Warnf("Failed to sync data to %s: status code %v", url, resp.Status())
	} else {
		logger.Infof("Successfully sync data to %s", url)
	}
}

func checkUserExists(t syncTarget, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) bool {
	if !t.checkEnabled || t.checkURL == "" {
		return true
	}

	url := replaceSyncURLPlaceholders(t.checkURL, userID, server, dataType)

	req := httpClient.R().SetHeaders(buildCheckHeaders(t))

	resp, err := req.Get(url)
	if err != nil {
		logger.Warnf("Check user failed for %s: %v", url, err)
		return false
	}
	if resp.StatusCode() == httpStatusOK {
		return true
	}
	if resp.StatusCode() == httpStatusNotFound {
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
	targets := buildSyncTargets(cfg, dataType, settings)

	if len(targets) == 0 {
		return
	}

	needsProcessed, needsRestored := computeProcessingNeeds(targets, dataType)

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

		if !checkUserExists(t, userID, server, dataType) {
			logger.Infof("Skipping sync to %s: user %d not found", t.url, userID)
			continue
		}

		data, encoding := chooseSyncPayload(
			t,
			dataType,
			rawData,
			processedData,
			restoredData,
			needsProcessed,
			needsRestored,
		)
		headers := buildSyncHeaders(t, userID, server, dataType)

		logger.Infof("Syncing %s data to %s...", dataType, t.url)
		go sendData(t.url, userID, server, dataType, data, encoding, headers)
	}
}
