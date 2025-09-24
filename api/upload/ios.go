package upload

import (
	"context"
	harukiRootApi "haruki-suite/api"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiHandler "haruki-suite/utils/handler"
	harukiLogger "haruki-suite/utils/logger"
	harukiMongo "haruki-suite/utils/mongo"
	"haruki-suite/utils/sekai"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

const chunkExpire = time.Minute * 3

var dataChunks map[string][]harukiUtils.DataChunk

type dataUploadHeader struct {
	ScriptVersion string                   `header:"X-Script-Version"`
	OriginalUrl   string                   `header:"X-Original-Url"`
	UploadId      string                   `header:"X-Upload-Id"`
	ChunkIndex    int                      `header:"X-Chunk-Index"`
	TotalChunks   int                      `header:"X-Total-Chunks"`
	Policy        harukiUtils.UploadPolicy `header:"X-Upload-Policy"`
}

func registerIOSRoutes(app *fiber.App, mongoManager *harukiMongo.MongoDBManager, redisClient *redis.Client) {
	api := app.Group("/ios")
	logger := harukiLogger.NewLogger("HarukiSekaiIOS", "DEBUG", nil)

	api.Post("/script/upload", func(c *fiber.Ctx) error {
		chunkIndex, _ := strconv.Atoi(c.Get("X-Chunk-Index", "0"))
		totalChunks, _ := strconv.Atoi(c.Get("X-Total-Chunks", "0"))
		header := &dataUploadHeader{
			ScriptVersion: c.Get("X-Script-Version"),
			OriginalUrl:   c.Get("X-Original-Url"),
			UploadId:      c.Get("X-Upload-Id"),
			ChunkIndex:    chunkIndex,
			TotalChunks:   totalChunks,
		}
		policyStr := c.Get("X-Upload-Policy")
		policy, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Invalid upload policy",
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}
		header.Policy = policy

		if header.ScriptVersion == "" {
			header.ScriptVersion = "unknown"
		}

		uploadType, userId := harukiRootApi.ExtractUploadTypeAndUserID(header.OriginalUrl)
		if uploadType == "" {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Unknown upload type",
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		var server harukiUtils.SupportedDataUploadServer
		for s, tuple := range sekai.GetAPIEndpoint() {
			if strings.Contains(header.OriginalUrl, tuple[1]) {
				server = s
				break
			}
		}
		if server == "" {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Unknown game server",
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
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

		// ✅ 先返回响应
		go func(header *dataUploadHeader, userId int64, server harukiUtils.SupportedDataUploadServer, uploadType string) {
			chunks := dataChunks[header.UploadId]
			if len(chunks) == header.TotalChunks {
				// 排序拼接
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
				_, err := HandleUpload(ctx, payload, string(server), string(header.Policy),
					mongoManager, redisClient, uploadType, &userId)
				if err != nil {
					logger.Errorf("HandleUpload failed: %v", err)
				}

				delete(dataChunks, header.UploadId)
			}
		}(header, userId, server, string(uploadType))

		// 马上返回
		return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{Message: "Successfully uploaded data."})
	})

	api.Get("/proxy/:server/:policy/suite/user/:user_id", func(c *fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		policyStr := c.Params("policy")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}
		policy, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		logger.Infof("Received %s server suite request from user %d", server, userID)

		dataHandler := harukiHandler.DataHandler{MongoManager: mongoManager, RestyClient: resty.New()}
		proxyHandler := sekai.HandleProxyUpload(
			mongoManager,
			harukiConfig.Cfg.Proxy,
			policy,
			dataHandler.PreHandleData,
			dataHandler.CallWebhook,
			redisClient,
			harukiUtils.UploadDataTypeSuite,
		)
		// python
		// for path in get_clear_cache_paths(server, UploadDataType.suite, user_id):
		// await clear_cache_by_path(**path)
		return proxyHandler(c)
	})

	api.Post("/proxy/:server/:policy/user/:user_id/mysekai", func(c *fiber.Ctx) error {
		userIDStr := c.Params("user_id")
		serverStr := c.Params("server")
		policyStr := c.Params("policy")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}
		policy, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}
		userID, err := strconv.ParseInt(userIDStr, 0, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		logger.Infof("Received %s server mysekai request from user %d", server, userID)

		dataHandler := harukiHandler.DataHandler{MongoManager: mongoManager, RestyClient: resty.New(), Logger: logger}
		proxyHandler := sekai.HandleProxyUpload(
			mongoManager,
			harukiConfig.Cfg.Proxy,
			policy,
			dataHandler.PreHandleData,
			dataHandler.CallWebhook,
			redisClient,
			harukiUtils.UploadDataTypeMysekai,
		)
		// python
		// for path in get_clear_cache_paths(server, UploadDataType.suite, user_id):
		// await clear_cache_by_path(**path)
		return proxyHandler(c)
	})
}
