package upload

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/iosscriptcode"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	maxDataChunksSize    = 64 * 1024 * 1024
	maxUploadChunkCount  = 1024
	maxUploadIDLength    = 128
	chunkUploadIDSepChar = "|"
	asyncUploadTimeout   = 2 * time.Minute
)

// scriptUploadAckWait bounds how long the final chunk's response waits for
// the upload to be processed. Processing usually takes well under a second
// (a large suite about 1 s at p99), and the proxy app holds the game's
// response while the script runs, so a slow upload answers 202 and finishes
// in the background instead of keeping the game waiting.
var scriptUploadAckWait = 15 * time.Second

// Each assembler can hold both a full upload's chunks and the joined body.
// Admit before reading any chunks from Redis and hold the slot until processing
// finishes, including when the client receives an early 202.
var iosUploadAssemblySlots = make(chan struct{}, 2)

// The indexed upload-code lookup runs before the per-owner quota so invalid
// credentials cannot spend a valid owner's budget on a shared proxy or NAT.
var iosUploadCredentialSlots = make(chan struct{}, 8)

func lookupIOSUploadCode(ctx context.Context, db *postgresql.Client, code string) (*postgresql.IOSScriptCode, error) {
	select {
	case iosUploadCredentialSlots <- struct{}{}:
		defer func() { <-iosUploadCredentialSlots }()
	default:
		return nil, errIOSUploadAssemblyRetry
	}
	return db.IOSScriptCode.Query().Where(iosscriptcode.UploadCodeEQ(code)).Only(ctx)
}

var errIOSUploadAssemblyRetry = errors.New("upload assembly can be retried")

type dataUploadHeader struct {
	ScriptVersion string `header:"X-Script-Version"`
	OriginalUrl   string `header:"X-Original-Url"`
	UploadId      string `header:"X-Upload-Id"`
	ChunkIndex    int    `header:"X-Chunk-Index"`
	TotalChunks   int    `header:"X-Total-Chunks"`
}

func validateDataUploadHeader(header *dataUploadHeader) error {
	uploadID := strings.TrimSpace(header.UploadId)
	if uploadID == "" {
		return fmt.Errorf("missing X-Upload-Id")
	}
	if len(uploadID) > maxUploadIDLength {
		return fmt.Errorf("X-Upload-Id is too long")
	}
	if header.TotalChunks < 1 || header.TotalChunks > maxUploadChunkCount {
		return fmt.Errorf("X-Total-Chunks must be between 1 and %d", maxUploadChunkCount)
	}
	if header.ChunkIndex < 0 || header.ChunkIndex >= header.TotalChunks {
		return fmt.Errorf("X-Chunk-Index is out of range")
	}
	return nil
}

func buildChunkUploadKey(toolboxUserID string, server harukiUtils.SupportedDataUploadServer, gameUserID int64, uploadID string) string {
	return fmt.Sprintf("%s%s%s%s%d%s%s", toolboxUserID, chunkUploadIDSepChar, server, chunkUploadIDSepChar, gameUserID, chunkUploadIDSepChar, uploadID)
}

func handleIOSProxySuite(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		userID, err := parseIOSProxyPathInt(userIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid user_id")
		}
		logger.Infof("Received %s server suite request from user %d", server, userID)
		proxyHandler := HandleProxyUpload(
			dependencies.Proxy,
			harukiUtils.UploadDataTypeSuite,
			apiHelper,
			dependencies,
			nil,
		)
		return proxyHandler(c)
	}
}

func handleIOSProxyMysekai(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		userID, err := parseIOSProxyPathInt(userIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid user_id")
		}
		logger.Infof("Received %s server mysekai request from user %d", server, userID)
		proxyHandler := HandleProxyUpload(
			dependencies.Proxy,
			harukiUtils.UploadDataTypeMysekai,
			apiHelper,
			dependencies,
			nil,
		)
		return proxyHandler(c)
	}
}

func handleIOSProxyMysekaiBirthdayPartyDelivery(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		partyIdStr := c.Params("party_id")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		userID, err := parseIOSProxyPathInt(userIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid user_id")
		}
		partyID, err := parseIOSProxyPathInt(partyIdStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid party_id")
		}
		logger.Infof("Received %s server mysekai birthday party delivery request from user %d for party id %d", server, userID, partyID)
		proxyHandler := HandleProxyUpload(
			dependencies.Proxy,
			harukiUtils.UploadDataTypeMysekaiBirthdayParty,
			apiHelper,
			dependencies,
			&partyID,
		)
		return proxyHandler(c)
	}
}

func retryIOSUploadAssembly(c fiber.Ctx) error {
	c.Set(fiber.HeaderRetryAfter, "3")
	return c.SendStatus(fiber.StatusTooManyRequests)
}

func handleIOSScriptUploadWithValidation(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		uploadCode := c.Params("upload_code")
		if uploadCode == "" || len(uploadCode) > 128 {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid upload_code")
		}
		record, err := lookupIOSUploadCode(ctx, apiHelper.DBManager.DB, uploadCode)
		if err != nil {
			if errors.Is(err, errIOSUploadAssemblyRetry) {
				return retryIOSUploadAssembly(c)
			}
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid upload code")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to validate upload code")
		}
		toolboxUserID := record.UserID
		if retryAfter := openUploadRetryAfter(ctx, apiHelper, iosScriptUploadRateBucket(toolboxUserID), time.Now().UTC()); retryAfter > 0 {
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(retryAfter))
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusTooManyRequests, "too many requests", nil)
		}
		chunkIndex64, err := strconv.ParseInt(c.Get("X-Chunk-Index", ""), 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid X-Chunk-Index")
		}
		totalChunks64, err := strconv.ParseInt(c.Get("X-Total-Chunks", ""), 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid X-Total-Chunks")
		}
		header := &dataUploadHeader{
			ScriptVersion: c.Get("X-Script-Version"),
			OriginalUrl:   c.Get("X-Original-Url"),
			UploadId:      c.Get("X-Upload-Id"),
			ChunkIndex:    int(chunkIndex64),
			TotalChunks:   int(totalChunks64),
		}
		if err := validateDataUploadHeader(header); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid upload headers")
		}
		if header.ScriptVersion == "" {
			header.ScriptVersion = "unknown"
		}
		uploadType, gameUserId := ExtractUploadTypeAndUserID(header.OriginalUrl)
		if uploadType == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "Unknown upload type")
		}
		var server harukiUtils.SupportedDataUploadServer
		for s, tuple := range sekai.GetAPIEndpoint() {
			if strings.Contains(header.OriginalUrl, tuple[1]) {
				server = s
				break
			}
		}
		if server == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "Unknown game server")
		}
		access, err := apiHelper.DBManager.DB.CanWriteGameAccountData(ctx, toolboxUserID, string(server), strconv.FormatInt(gameUserId, 10), string(uploadType), time.Now().UTC())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to check upload permission")
		}
		if !access.Allowed {
			return harukiAPIHelper.ErrorForbidden(c, "upload not allowed")
		}
		body := c.Request().Body()
		if len(body) == 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "empty upload body")
		}
		if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
			return harukiAPIHelper.ErrorInternal(c, "upload store unavailable")
		}
		redisClient := apiHelper.DBManager.Redis.Redis
		uploadKey := buildChunkUploadKey(toolboxUserID, server, gameUserId, fmt.Sprintf("%d:%x:%s:%s", record.ID, sha256.Sum256([]byte(record.UploadCode)), uploadType, header.UploadId))
		persistResult, err := persistIOSUploadChunk(ctx, redisClient, uploadKey, header.TotalChunks, header.ChunkIndex, body)
		if err != nil {
			logger.Errorf("Failed to persist upload chunk for %s: %v", uploadKey, err)
			return harukiAPIHelper.ErrorInternal(c, "failed to store upload chunk")
		}
		switch persistResult.State {
		case iosUploadChunkStateInconsistentTotal:
			return harukiAPIHelper.ErrorBadRequest(c, "inconsistent X-Total-Chunks for this upload")
		case iosUploadChunkStateTooLarge:
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusRequestEntityTooLarge, "upload is too large", nil)
		case iosUploadChunkStateBudgetExceeded:
			c.Set("Retry-After", "30")
			return c.SendStatus(fiber.StatusTooManyRequests)
		case iosUploadChunkStateCompleteAlreadyClaimed:
			return retryIOSUploadAssembly(c)
		case iosUploadChunkStateIncomplete:
			return harukiAPIHelper.Responses.SuccessResponse[string](c, "Successfully uploaded data.", nil)
		}

		select {
		case iosUploadAssemblySlots <- struct{}{}:
		default:
			return retryIOSUploadAssembly(c)
		}
		slotTransferred := false
		defer func() {
			if !slotTransferred {
				<-iosUploadAssemblySlots
			}
		}()
		claim := uuid.NewString()
		assemblyCtx, cancelAssembly := context.WithTimeout(ctx, 7*time.Second)
		defer cancelAssembly()
		claimed, err := claimIOSUploadChunks(assemblyCtx, redisClient, uploadKey, header.TotalChunks, claim)
		if err != nil || !claimed {
			return retryIOSUploadAssembly(c)
		}
		releaseClaim := func() {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := resetIOSUploadClaim(cleanup, redisClient, uploadKey, claim); err != nil {
				logger.Warnf("Failed to reset upload claim for %s: %v", uploadKey, err)
			}
		}
		completedChunks, err := loadIOSUploadChunks(assemblyCtx, redisClient, uploadKey, header.TotalChunks)
		if err != nil {
			releaseClaim()
			logger.Errorf("Failed to load completed upload chunks for %s: %v", uploadKey, err)
			return retryIOSUploadAssembly(c)
		}

		totalChunks := header.TotalChunks
		codeID, codeValue := record.ID, strings.Clone(record.UploadCode)
		toolboxUserIDCopy := toolboxUserID
		attempt := newAttempt(harukiUtils.UploadMethodIOSScript, 0)
		processed := make(chan error, 1)
		accepted := startBackgroundTask(dependencies.BackgroundTasks, logger, "ios-upload-assembly", func() {
			defer func() { <-iosUploadAssemblySlots }()
			cleanup, cancelCleanup := context.WithTimeout(context.Background(), 3*time.Second)
			err := clearIOSUploadChunks(cleanup, redisClient, uploadKey, totalChunks, claim)
			cancelCleanup()
			if err != nil {
				releaseClaim()
				logger.Warnf("Failed to consume completed upload chunks for %s: %v", uploadKey, err)
				processed <- errIOSUploadAssemblyRetry
				return
			}
			chunks := completedChunks
			sort.Slice(chunks, func(x, y int) bool {
				return chunks[x].ChunkIndex < chunks[y].ChunkIndex
			})
			totalLen := 0
			for _, c := range chunks {
				totalLen += len(c.Data)
			}
			payload := make([]byte, totalLen)
			offset := 0
			for _, c := range chunks {
				copy(payload[offset:], c.Data)
				offset += len(c.Data)
			}
			uploadCtx, cancel := context.WithTimeout(context.Background(), asyncUploadTimeout)
			defer cancel()
			// The assembly itself was admitted before shutdown sealing. Keep all
			// follow-up audit/fanout work inside this tracked parent so it cannot
			// race task-group shutdown by attempting nested admission later.
			innerDependencies := dependencies
			innerDependencies.BackgroundTasks = harukiBackground.InlineRunner{Shutdown: harukiBackground.ShutdownSignal(dependencies.BackgroundTasks)}
			innerDependencies.ValidateUploadIdentity = func(ctx context.Context) error {
				current, err := apiHelper.DBManager.DB.IOSScriptCode.Get(ctx, codeID)
				if postgresql.IsNotFound(err) {
					return errUploadOwnershipMismatch
				}
				if err != nil {
					return err
				}
				if current.UserID != toolboxUserIDCopy || subtle.ConstantTimeCompare([]byte(current.UploadCode), []byte(codeValue)) != 1 {
					return errUploadOwnershipMismatch
				}
				return nil
			}
			attempt.RequestBytes = int64(len(payload))
			_, err = runUpload(uploadCtx, payload, server, harukiUtils.UploadDataType(uploadType), &gameUserId, &toolboxUserIDCopy, apiHelper, innerDependencies, harukiUtils.UploadMethodIOSScript, attempt)
			if _, skipped := uploadSkipReason(err); err != nil && !skipped {
				// HandleUpload already logs every failure with full context at
				// WARNING inside its fail() helper; re-logging at ERROR here
				// duplicated the line and inflated the error rate with routine
				// user-input failures.
				logger.Debugf("HandleUpload finished with error: %v", err)
			}
			processed <- err
		})
		if !accepted {
			releaseClaim()
			return retryIOSUploadAssembly(c)
		}
		slotTransferred = true
		// The upload runs as a tracked background task so shutdown drains it;
		// the response waits for its result, up to scriptUploadAckWait.
		timer := time.NewTimer(scriptUploadAckWait)
		defer timer.Stop()
		select {
		case err := <-processed:
			if errors.Is(err, errIOSUploadAssemblyRetry) {
				return retryIOSUploadAssembly(c)
			}
			if reason, skipped := uploadSkipReason(err); skipped {
				return uploadSkippedResponse(c, reason)
			}
			if err != nil {
				status, code := uploadFailure(attempt)
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, status, code, nil)
			}
			return harukiAPIHelper.Responses.SuccessResponse[string](c, "Successfully uploaded data.", nil)
		case <-timer.C:
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusAccepted, "upload received, still processing", nil)
		}
	}
}

func parseIOSProxyPathInt(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}

func registerIOSUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	logger := harukiLogger.NewLoggerFromGlobal("HarukiSekaiIOS")
	// A refused proxy upload still says so to the module's notification script.
	proxyGuard := openUploadEntryGuard(apiHelper, func(c fiber.Ctx) {
		setProxyUploadResult(c, proxyUploadFailed, "rate_limited")
	})
	for _, prefix := range []string{"/ios", "/api/ios"} {
		api := apiHelper.Router.Group(prefix)

		api.Post("/script/:upload_code/upload", handleIOSScriptUploadWithValidation(apiHelper, dependencies, logger))
		api.Get("/proxy/:server/suite/user/:user_id", proxyGuard, handleIOSProxySuite(apiHelper, dependencies, logger))
		api.Post("/proxy/:server/user/:user_id/mysekai", proxyGuard, handleIOSProxyMysekai(apiHelper, dependencies, logger))
		api.Put("/proxy/:server/user/:user_id/mysekai/birthday-party/:party_id/delivery", proxyGuard, handleIOSProxyMysekaiBirthdayPartyDelivery(apiHelper, dependencies, logger))
	}
}
