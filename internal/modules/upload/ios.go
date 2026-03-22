package upload

import (
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekai"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	maxDataChunksSize    = 64 * 1024 * 1024
	maxUploadChunkCount  = 1024
	maxUploadIDLength    = 128
	chunkUploadIDSepChar = "|"
	asyncUploadTimeout   = 2 * time.Minute
)

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

func handleIOSProxySuite(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, logger *harukiLogger.Logger) fiber.Handler {
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
			harukiConfig.Cfg.Proxy,
			harukiUtils.UploadDataTypeSuite,
			apiHelper,
			nil,
		)
		return proxyHandler(c)
	}
}

func handleIOSProxyMysekai(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, logger *harukiLogger.Logger) fiber.Handler {
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
			harukiConfig.Cfg.Proxy,
			harukiUtils.UploadDataTypeMysekai,
			apiHelper,
			nil,
		)
		return proxyHandler(c)
	}
}

func handleIOSProxyMysekaiBirthdayPartyDelivery(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, logger *harukiLogger.Logger) fiber.Handler {
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
			harukiConfig.Cfg.Proxy,
			harukiUtils.UploadDataTypeMysekaiBirthdayParty,
			apiHelper,
			&partyID,
		)
		return proxyHandler(c)
	}
}

func handleIOSScriptUploadWithValidation(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		uploadCode := c.Params("upload_code")
		if uploadCode == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing upload_code")
		}
		record, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UploadCodeEQ(uploadCode)).
			Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid upload code")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to validate upload code")
		}
		toolboxUserID := record.UserID
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
		bindings, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(gameaccountbinding.HasUserWith(user.IDEQ(toolboxUserID))).
			Where(gameaccountbinding.ServerEQ(string(server))).
			Where(gameaccountbinding.VerifiedEQ(true)).
			All(ctx)
		if err != nil || len(bindings) == 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "No verified game account binding found for this server")
		}
		gameUserIdStr := strconv.FormatInt(gameUserId, 10)
		matched := false
		for _, binding := range bindings {
			if binding.GameUserID == gameUserIdStr {
				matched = true
				break
			}
		}
		if !matched {
			return harukiAPIHelper.ErrorBadRequest(c, "Game user ID does not match your bound accounts")
		}
		body := c.Request().Body()
		if len(body) == 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "empty upload body")
		}
		if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
			return harukiAPIHelper.ErrorInternal(c, "upload store unavailable")
		}
		redisClient := apiHelper.DBManager.Redis.Redis
		uploadKey := buildChunkUploadKey(toolboxUserID, server, gameUserId, header.UploadId)
		persistResult, err := persistIOSUploadChunk(ctx, redisClient, uploadKey, header.TotalChunks, header.ChunkIndex, body)
		if err != nil {
			logger.Errorf("Failed to persist upload chunk for %s: %v", uploadKey, err)
			return harukiAPIHelper.ErrorInternal(c, "failed to store upload chunk")
		}
		switch persistResult.State {
		case iosUploadChunkStateInconsistentTotal:
			return harukiAPIHelper.ErrorBadRequest(c, "inconsistent X-Total-Chunks for this upload")
		case iosUploadChunkStateTooLarge:
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusRequestEntityTooLarge, "upload is too large", nil)
		case iosUploadChunkStateIncomplete, iosUploadChunkStateCompleteAlreadyClaimed:
			return harukiAPIHelper.SuccessResponse[string](c, "Successfully uploaded data.", nil)
		}

		completedChunks, err := loadIOSUploadChunks(ctx, redisClient, uploadKey, header.TotalChunks)
		if err != nil {
			if resetErr := resetIOSUploadClaim(ctx, redisClient, uploadKey); resetErr != nil {
				logger.Warnf("Failed to reset upload claim for %s: %v", uploadKey, resetErr)
			}
			logger.Errorf("Failed to load completed upload chunks for %s: %v", uploadKey, err)
			return harukiAPIHelper.ErrorInternal(c, "failed to assemble upload chunks")
		}
		if err := clearIOSUploadChunks(ctx, redisClient, uploadKey); err != nil {
			logger.Warnf("Failed to clear completed upload chunks for %s: %v", uploadKey, err)
		}

		toolboxUserIDCopy := toolboxUserID
		go func(chunks []harukiUtils.DataChunk, userId int64, server harukiUtils.SupportedDataUploadServer, uploadType string, toolboxUserID string) {
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
			_, err := HandleUpload(uploadCtx, payload, server, harukiUtils.UploadDataType(uploadType), &userId, &toolboxUserID, apiHelper, harukiUtils.UploadMethodIOSScript)
			if err != nil {
				logger.Errorf("HandleUpload failed: %v", err)
			}
		}(completedChunks, gameUserId, server, string(uploadType), toolboxUserIDCopy)
		return harukiAPIHelper.SuccessResponse[string](c, "Successfully uploaded data.", nil)
	}
}

func parseIOSProxyPathInt(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}

func registerIOSUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	logger := harukiLogger.NewLoggerFromGlobal("HarukiSekaiIOS")
	proxyGuard := openUploadEntryGuard(apiHelper)
	for _, prefix := range []string{"/ios", "/api/ios"} {
		api := apiHelper.Router.Group(prefix)

		api.Post("/script/:upload_code/upload", handleIOSScriptUploadWithValidation(apiHelper, logger))
		api.Get("/proxy/:server/suite/user/:user_id", proxyGuard, handleIOSProxySuite(apiHelper, logger))
		api.Post("/proxy/:server/user/:user_id/mysekai", proxyGuard, handleIOSProxyMysekai(apiHelper, logger))
		api.Put("/proxy/:server/user/:user_id/mysekai/birthday-party/:party_id/delivery", proxyGuard, handleIOSProxyMysekaiBirthdayPartyDelivery(apiHelper, logger))
	}
}
