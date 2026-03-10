package upload

import (
	"context"
	"fmt"
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
	dataChunkTotals = make(map[string]int)
	dataChunksMutex sync.RWMutex
	dataChunksSize  int64
)

const (
	maxDataChunksSize    = 64 * 1024 * 1024
	maxUploadChunkCount  = 1024
	maxUploadIDLength    = 128
	chunkUploadIDSepChar = "|"
	asyncUploadTimeout   = 2 * time.Minute
)

func init() {
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
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
			latestActivity := chunks[0].Time
			for _, chunk := range chunks[1:] {
				if chunk.Time.After(latestActivity) {
					latestActivity = chunk.Time
				}
			}
			if now.Sub(latestActivity) > 5*time.Minute {
				for _, chunk := range chunks {
					dataChunksSize -= int64(len(chunk.Data))
				}
				delete(dataChunks, uploadID)
				delete(dataChunkTotals, uploadID)
			}
		} else {
			delete(dataChunks, uploadID)
			delete(dataChunkTotals, uploadID)
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

func hasAllChunks(chunks []harukiUtils.DataChunk, totalChunks int) bool {
	if len(chunks) != totalChunks {
		return false
	}
	seen := make(map[int]struct{}, len(chunks))
	for _, chunk := range chunks {
		if chunk.ChunkIndex < 0 || chunk.ChunkIndex >= totalChunks {
			return false
		}
		seen[chunk.ChunkIndex] = struct{}{}
	}
	return len(seen) == totalChunks
}

func handleIOSProxySuite(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, logger *harukiLogger.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		userID, err := parseIOSProxyPathInt(userIDStr)
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
		userID, err := parseIOSProxyPathInt(userIDStr)
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
		userID, err := parseIOSProxyPathInt(userIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		partyID, err := parseIOSProxyPathInt(partyIdStr)
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
		chunkIndex, err := strconv.Atoi(c.Get("X-Chunk-Index", ""))
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid X-Chunk-Index")
		}
		totalChunks, err := strconv.Atoi(c.Get("X-Total-Chunks", ""))
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid X-Total-Chunks")
		}
		header := &dataUploadHeader{
			ScriptVersion: c.Get("X-Script-Version"),
			OriginalUrl:   c.Get("X-Original-Url"),
			UploadId:      c.Get("X-Upload-Id"),
			ChunkIndex:    chunkIndex,
			TotalChunks:   totalChunks,
		}
		if err := validateDataUploadHeader(header); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
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
		if len(body) == 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "empty upload body")
		}
		chunkCopy := make([]byte, len(body))
		copy(chunkCopy, body)
		uploadKey := buildChunkUploadKey(toolboxUserID, server, gameUserId, header.UploadId)
		dataChunksMutex.Lock()
		if expectedTotal, ok := dataChunkTotals[uploadKey]; ok {
			if expectedTotal != header.TotalChunks {
				dataChunksMutex.Unlock()
				return harukiAPIHelper.ErrorBadRequest(c, "inconsistent X-Total-Chunks for this upload")
			}
		} else {
			dataChunkTotals[uploadKey] = header.TotalChunks
		}
		chunks := dataChunks[uploadKey]
		replacedIndex := -1
		oldChunkLen := 0
		for i := range chunks {
			if chunks[i].ChunkIndex == header.ChunkIndex {
				replacedIndex = i
				oldChunkLen = len(chunks[i].Data)
				break
			}
		}
		sizeDelta := int64(len(chunkCopy) - oldChunkLen)
		if dataChunksSize+sizeDelta > maxDataChunksSize {
			dataChunksMutex.Unlock()
			return harukiAPIHelper.ErrorInternal(c, "Server upload buffer full, try again later")
		}
		dataChunksSize += sizeDelta
		newChunk := harukiUtils.DataChunk{
			ChunkIndex: header.ChunkIndex,
			Data:       chunkCopy,
			Time:       now,
		}
		if replacedIndex >= 0 {
			chunks[replacedIndex] = newChunk
		} else {
			chunks = append(chunks, newChunk)
		}
		dataChunks[uploadKey] = chunks
		var completedChunks []harukiUtils.DataChunk
		if hasAllChunks(chunks, header.TotalChunks) {
			completedChunks = append(completedChunks, chunks...)
			for _, chunk := range completedChunks {
				dataChunksSize -= int64(len(chunk.Data))
			}
			delete(dataChunks, uploadKey)
			delete(dataChunkTotals, uploadKey)
		}
		dataChunksMutex.Unlock()
		if completedChunks != nil {
			completedChunksCopy := cloneDataChunks(completedChunks)
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
			}(completedChunksCopy, gameUserId, server, string(uploadType), toolboxUserIDCopy)
		}
		return harukiAPIHelper.SuccessResponse[string](c, "Successfully uploaded data.", nil)
	}
}

func parseIOSProxyPathInt(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}

func cloneDataChunks(chunks []harukiUtils.DataChunk) []harukiUtils.DataChunk {
	cloned := make([]harukiUtils.DataChunk, 0, len(chunks))
	for _, chunk := range chunks {
		dataCopy := make([]byte, len(chunk.Data))
		copy(dataCopy, chunk.Data)
		cloned = append(cloned, harukiUtils.DataChunk{
			ChunkIndex: chunk.ChunkIndex,
			Data:       dataCopy,
			Time:       chunk.Time,
		})
	}
	return cloned
}

func registerIOSUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/ios")
	logger := harukiLogger.NewLogger("HarukiSekaiIOS", "DEBUG", nil)
	proxyGuard := openUploadEntryGuard(apiHelper)

	api.Post("/script/:upload_code/upload", handleIOSScriptUploadWithValidation(apiHelper, logger))
	api.Get("/proxy/:server/suite/user/:user_id", proxyGuard, handleIOSProxySuite(apiHelper, logger))
	api.Post("/proxy/:server/user/:user_id/mysekai", proxyGuard, handleIOSProxyMysekai(apiHelper, logger))
	api.Put("/proxy/:server/user/:user_id/mysekai/birthday-party/:party_id/delivery", proxyGuard, handleIOSProxyMysekaiBirthdayPartyDelivery(apiHelper, logger))
}
