package upload

import (
	"context"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekai"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

var (
	dataChunks      = make(map[string][]harukiUtils.DataChunk)
	dataChunksMutex sync.RWMutex
	dataChunksSize  int64
)

const maxDataChunksSize = 16 * 1024 * 1024

func init() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		for range ticker.C {
			cleanExpiredChunks()
		}
	}()
}

func cleanExpiredChunks() {
	dataChunksMutex.Lock()
	defer dataChunksMutex.Unlock()
	now := time.Now()
	for uploadID, chunks := range dataChunks {
		if len(chunks) > 0 {
			if now.Sub(chunks[len(chunks)-1].Time) > 30*time.Minute {
				for _, chunk := range chunks {
					dataChunksSize -= int64(len(chunk.Data))
				}
				delete(dataChunks, uploadID)
			}
		} else {
			delete(dataChunks, uploadID)
		}
	}
}

type dataUploadHeader struct {
	ScriptVersion string `header:"X-Script-Version"`
	OriginalUrl   string `header:"X-Original-Url"`
	UploadId      string `header:"X-Upload-Id"`
	ChunkIndex    int    `header:"X-Chunk-Index"`
	TotalChunks   int    `header:"X-Total-Chunks"`
}

func handleIOSProxySuite(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
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
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
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
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		partyID, err := strconv.ParseInt(partyIdStr, 0, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
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
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid upload code")
		}
		toolboxUserID := record.UserID
		chunkIndex, _ := strconv.Atoi(c.Get("X-Chunk-Index", "0"))
		totalChunks, _ := strconv.Atoi(c.Get("X-Total-Chunks", "0"))
		header := &dataUploadHeader{
			ScriptVersion: c.Get("X-Script-Version"),
			OriginalUrl:   c.Get("X-Original-Url"),
			UploadId:      c.Get("X-Upload-Id"),
			ChunkIndex:    chunkIndex,
			TotalChunks:   totalChunks,
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
		now := time.Now()
		body := c.Request().Body()
		chunkCopy := make([]byte, len(body))
		copy(chunkCopy, body)
		dataChunksMutex.Lock()
		if dataChunksSize+int64(len(chunkCopy)) > maxDataChunksSize {
			dataChunksMutex.Unlock()
			return harukiAPIHelper.ErrorInternal(c, "Server upload buffer full, try again later")
		}
		dataChunksSize += int64(len(chunkCopy))
		dataChunks[header.UploadId] = append(dataChunks[header.UploadId], harukiUtils.DataChunk{
			ChunkIndex: header.ChunkIndex,
			Data:       chunkCopy,
			Time:       now,
		})
		var completedChunks []harukiUtils.DataChunk
		if len(dataChunks[header.UploadId]) == header.TotalChunks {
			completedChunks = dataChunks[header.UploadId]
			for _, chunk := range completedChunks {
				dataChunksSize -= int64(len(chunk.Data))
			}
			delete(dataChunks, header.UploadId)
		}
		dataChunksMutex.Unlock()
		if completedChunks != nil {
			go func(reqCtx context.Context, chunks []harukiUtils.DataChunk, userId int64, server harukiUtils.SupportedDataUploadServer, uploadType string) {
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
				ctx := context.WithoutCancel(reqCtx)
				_, err := HandleUpload(ctx, payload, server, harukiUtils.UploadDataType(uploadType), &userId, &toolboxUserID, apiHelper, harukiUtils.UploadMethodIOSScript)
				if err != nil {
					logger.Errorf("HandleUpload failed: %v", err)
				}
			}(c.Context(), completedChunks, gameUserId, server, string(uploadType))
		}
		return harukiAPIHelper.SuccessResponse[string](c, "Successfully uploaded data.", nil)
	}
}

func registerIOSUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/ios")
	logger := harukiLogger.NewLogger("HarukiSekaiIOS", "DEBUG", nil)

	api.Post("/script/:upload_code/upload", handleIOSScriptUploadWithValidation(apiHelper, logger))
	api.Get("/proxy/:server/suite/user/:user_id", handleIOSProxySuite(apiHelper, logger))
	api.Post("/proxy/:server/user/:user_id/mysekai", handleIOSProxyMysekai(apiHelper, logger))
	api.Put("/proxy/:server/user/:user_id/mysekai/birthday-party/:party_id/delivery", handleIOSProxyMysekaiBirthdayPartyDelivery(apiHelper, logger))
}
