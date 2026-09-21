package upload

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"sync"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiAPIData "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/msgpackcodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/nuverserestore"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/perfstats"
	harukiVersion "github.com/Team-Haruki/Haruki-Toolbox-Backend/version"
	"github.com/go-resty/resty/v2"
	"github.com/klauspost/compress/zstd"
)

var (
	logger     = harukiLogger.NewLoggerFromGlobal("HarukiDataSyncer")
	httpClient *resty.Client
)

func init() {
	httpClient = jsoncodec.ConfigureResty(resty.New())
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

var bytesBufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

func processMsgpackOnce(msgpackBytes []byte) ([]byte, error) {
	defer perfstats.Track(perfstats.SyncProcessed)()
	return compressSyncJSON(func(w io.Writer) error {
		return msgpackcodec.WriteJSON(w, msgpackBytes, harukiAPIData.ProviderJSONOptions())
	})
}

func processRestoredMsgpack(msgpackBytes []byte, server utils.SupportedDataUploadServer, service *SuiteRestoreService) ([]byte, error) {
	defer perfstats.Track(perfstats.SyncRestored)()
	unpacked, err := sekai.UnpackMsgpack(msgpackBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack data: %w", err)
	}
	data, ok := unpacked.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unpacked data is not a map")
	}
	data, err = service.MysekaiRestorer().Document(string(server), data)
	if err != nil {
		return nil, err
	}
	restored, _, err := service.Restore(server, data, SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync})
	if err != nil {
		return nil, fmt.Errorf("failed to restore suite data: %w", err)
	}
	return compressSyncJSON(func(w io.Writer) error {
		return json.MarshalWrite(w, harukiAPIData.NormalizeProviderResponse(restored))
	})
}

// Keep pooled output storage bounded, and detach the encoder from the buffer.
// The normalization contract is unchanged; only the intermediate JSON is removed.
func compressSyncJSON(write func(io.Writer) error) ([]byte, error) {
	buf := bytesBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	encoder := zstdEncoderPool.Get().(*zstd.Encoder)
	encoder.Reset(buf)
	defer func() {
		encoder.Reset(io.Discard)
		zstdEncoderPool.Put(encoder)
		if buf.Cap() <= 1<<20 {
			buf.Reset()
			bytesBufferPool.Put(buf)
		}
	}()
	writeErr := write(encoder)
	closeErr := encoder.Close()
	if writeErr != nil {
		return nil, fmt.Errorf("failed to encode json+zstd: %w", writeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("failed to close zstd writer: %w", closeErr)
	}
	return bytes.Clone(buf.Bytes()), nil
}

func sendData(url string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, data []byte, encoding string, headers map[string]string) {
	defer perfstats.Track(perfstats.SyncDelivery)()
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

func checkUserExists(ctx context.Context, t syncTarget, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) bool {
	defer perfstats.Track(perfstats.SyncCheck)()
	if !t.checkEnabled || t.checkURL == "" {
		return true
	}

	url := replaceSyncURLPlaceholders(t.checkURL, userID, server, dataType)

	req := httpClient.R().SetContext(ctx).SetHeaders(buildCheckHeaders(t))

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

func DataSyncer(
	userID int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
	rawData []byte,
	settings apiHelper.HarukiToolboxGameAccountPrivacySettings,
	serverCryptor sekai.ServerCryptor,
	suiteRestoreService *SuiteRestoreService,
) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("DataSyncer panicked: %v", r)
		}
	}()

	runDataSyncer(
		harukiConfig.Cfg.ThirdPartyDataProvider,
		userID,
		server,
		dataType,
		rawData,
		settings,
		serverCryptor,
		suiteRestoreService,
		sendData,
	)
}

type syncDataSender func(string, int64, utils.SupportedDataUploadServer, utils.UploadDataType, []byte, string, map[string]string)

func runDataSyncer(
	cfg harukiConfig.ThirdPartyDataProviderConfig,
	userID int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
	rawData []byte,
	settings apiHelper.HarukiToolboxGameAccountPrivacySettings,
	serverCryptor sekai.ServerCryptor,
	suiteRestoreService *SuiteRestoreService,
	sender syncDataSender,
) {
	runDataSyncerTargets(buildSyncTargets(cfg, dataType, settings), userID, server, dataType, rawData, serverCryptor, suiteRestoreService, sender)
}

var syncEncodingSlots = make(chan struct{}, 2)

func runDataSyncerTargets(targets []syncTarget, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, rawData []byte, serverCryptor sekai.ServerCryptor, suiteRestoreService *SuiteRestoreService, sender syncDataSender) {
	targets = eligibleSyncTargets(targets, userID, server, dataType)
	if len(targets) == 0 {
		return
	}
	needsProcessed, needsRestored := computeProcessingNeeds(targets, dataType)
	var processedData, restoredData []byte
	if needsProcessed || needsRestored {
		func() {
			stopWait := perfstats.Track(perfstats.SyncEncodeWait)
			syncEncodingSlots <- struct{}{}
			stopWait()
			defer func() { <-syncEncodingSlots }()
			// This owned byte slice is shared only between synchronous format encoders.
			// Never reuse the DB-preprocessed map or a released cryptor buffer.
			msgpackBytes, err := serverCryptor.DecryptToMsgpack(rawData, server)
			if err != nil {
				logger.Warnf("Failed to decrypt sync data: %v", err)
				needsProcessed, needsRestored = false, false
				return
			}
			if needsProcessed {
				if suiteRestoreService.MysekaiRestorer().Fingerprint(string(server)) != "" && (dataType == utils.UploadDataTypeMysekai || dataType == utils.UploadDataTypeMysekaiBirthdayParty) {
					processedData, err = processMysekaiMsgpack(msgpackBytes, string(server), suiteRestoreService.MysekaiRestorer())
				} else {
					processedData, err = processMsgpackOnce(msgpackBytes)
				}
				if err != nil {
					logger.Warnf("Failed to pre-process data: %v", err)
					needsProcessed = false
				}
			}
			if needsRestored {
				restoredData, err = processRestoredMsgpack(msgpackBytes, server, suiteRestoreService)
				if err != nil {
					logger.Warnf("Failed to process data with restore: %v", err)
					needsRestored = false
				}
			}
		}()
	}

	var sendTasks sync.WaitGroup
	for _, t := range targets {
		t := t

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
		sendTasks.Add(1)
		go func() {
			defer sendTasks.Done()
			sender(t.url, userID, server, dataType, data, encoding, headers)
		}()
	}
	sendTasks.Wait()
}

// At most four configured targets, with one shared deadline. A recipient rejected
// by its existence check never triggers decryption or format construction.
func eligibleSyncTargets(targets []syncTarget, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) []syncTarget {
	ctx, cancel := context.WithTimeout(context.Background(), dataSyncerTimeoutSeconds*time.Second)
	defer cancel()
	allowed := make([]bool, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Go(func() { allowed[i] = checkUserExists(ctx, target, userID, server, dataType) })
	}
	wg.Wait()
	out := make([]syncTarget, 0, len(targets))
	for i, target := range targets {
		if allowed[i] {
			out = append(out, target)
		}
	}
	return out
}

// Enabled MYSEKAI JSON consumers receive the same shape as database API consumers.
func processMysekaiMsgpack(payload []byte, server string, restorer *nuverserestore.MysekaiRestorer) ([]byte, error) {
	unpacked, err := sekai.UnpackMsgpack(payload)
	if err != nil {
		return nil, err
	}
	data, ok := unpacked.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unpacked mysekai data is not a map")
	}
	restored, err := restorer.Document(server, data)
	if err != nil {
		return nil, err
	}
	return compressSyncJSON(func(w io.Writer) error {
		return json.MarshalWrite(w, harukiAPIData.NormalizeProviderResponse(restored))
	})
}
