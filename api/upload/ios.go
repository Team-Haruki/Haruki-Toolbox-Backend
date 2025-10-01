package upload

import (
	"context"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekai"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const chunkExpire = time.Minute * 3

var dataChunks map[string][]harukiUtils.DataChunk

type dataUploadHeader struct {
	ScriptVersion string `header:"X-Script-Version"`
	OriginalUrl   string `header:"X-Original-Url"`
	UploadId      string `header:"X-Upload-Id"`
	ChunkIndex    int    `header:"X-Chunk-Index"`
	TotalChunks   int    `header:"X-Total-Chunks"`
}

func registerIOSUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/ios")
	logger := harukiLogger.NewLogger("HarukiSekaiIOS", "DEBUG", nil)

	api.Post("/script/upload", func(c *fiber.Ctx) error {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "This endpoint is temporarily disabled", nil)
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

		uploadType, userId := ExtractUploadTypeAndUserID(header.OriginalUrl)
		if uploadType == "" {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Unknown upload type", nil)
		}

		var server harukiUtils.SupportedDataUploadServer
		for s, tuple := range sekai.GetAPIEndpoint() {
			if strings.Contains(header.OriginalUrl, tuple[1]) {
				server = s
				break
			}
		}
		if server == "" {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Unknown game server", nil)
		}

		now := time.Now()
		body := c.Request().Body()

		if dataChunks == nil {
			dataChunks = make(map[string][]harukiUtils.DataChunk)
		}
		chunkCopy := make([]byte, len(body))
		copy(chunkCopy, body)
		dataChunks[header.UploadId] = append(dataChunks[header.UploadId], harukiUtils.DataChunk{
			RequestURL:  header.OriginalUrl,
			UploadID:    header.UploadId,
			ChunkIndex:  header.ChunkIndex,
			TotalChunks: header.TotalChunks,
			Time:        now,
			Data:        chunkCopy,
		})

		go func(header *dataUploadHeader, userId int64, server harukiUtils.SupportedDataUploadServer, uploadType string) {
			chunks := dataChunks[header.UploadId]
			if len(chunks) == header.TotalChunks {
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

				ctx := context.Background()
				_, err := HandleUpload(ctx, payload, server, harukiUtils.UploadDataType(uploadType), &userId, nil, apiHelper)
				if err != nil {
					logger.Errorf("HandleUpload failed: %v", err)
				}

				delete(dataChunks, header.UploadId)
			}
		}(header, userId, server, string(uploadType))

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "Successfully uploaded data.", nil)
	})

	api.Get("/proxy/:server/suite/user/:user_id", func(c *fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		logger.Infof("Received %s server suite request from user %d", server, userID)

		proxyHandler := HandleProxyUpload(
			harukiConfig.Cfg.Proxy,
			harukiUtils.UploadDataTypeSuite,
			apiHelper,
		)
		return proxyHandler(c)
	})

	api.Post("/proxy/:server/user/:user_id/mysekai", func(c *fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		logger.Infof("Received %s server mysekai request from user %d", server, userID)

		proxyHandler := HandleProxyUpload(
			harukiConfig.Cfg.Proxy,
			harukiUtils.UploadDataTypeMysekai,
			apiHelper,
		)
		return proxyHandler(c)
	})
}
